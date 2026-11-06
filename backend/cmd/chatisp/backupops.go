package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"chatisp/internal/attachments"
)

// Object keys are an opaque tenant namespace and a random id: two UUIDs. The
// mirror refuses anything else, so a key can never leave the destination tree.
var objectKeyPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const (
	objectsDir       = "objects"
	manifestName     = "objects.manifest"
	lockName         = ".mirror.lock"
	manifestHeader   = "# attachments-mirror-1 key<TAB>version<TAB>sha256<TAB>bytes"
	manifestFooter   = "# end-attachments-mirror-1"
	maxMirrorObjects = 1 << 20
)

type mirrorEntry struct {
	Key, Version, SHA string
	Bytes             int64
}

func mirrorStorage(environ []string) (attachments.Storage, attachments.Walker, error) {
	storage, err := attachmentStorage(environ)
	if err != nil {
		return nil, nil, err
	}
	walker, ok := storage.(attachments.Walker)
	if !ok {
		return nil, nil, errors.New("armazenamento de anexos não configurado: defina CHATISP_ATTACHMENTS_S3_*")
	}
	if s3, ok := storage.(*attachments.S3Storage); !ok || !s3.Unversioned() {
		return nil, nil, errors.New("o espelho de anexos só trata bucket sem versionamento (CHATISP_ATTACHMENTS_S3_UNVERSIONED=true); em bucket versionado use a replicação nativa do provedor, pois os version ids mudariam na restauração")
	}
	return storage, walker, nil
}

// attachmentsBackup mirrors every stored attachment object into a directory.
// It is append-only: it never deletes or overwrites a mirrored object, so an
// accidental or privacy deletion in the live bucket does not propagate to the
// copy. Exclusions are reapplied on restore from the deletion ledger.
func attachmentsBackup(ctx context.Context, args, environ []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("attachments-backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dest := flags.String("dest", "", "diretório de destino (outro disco/host montado)")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *dest == "" {
		return errors.New("uso: chatisp attachments-backup -dest DIR")
	}
	storage, walker, err := mirrorStorage(environ)
	if err != nil {
		return err
	}
	return attachmentsBackupWith(ctx, *dest, storage, walker, stdout)
}

func attachmentsBackupWith(ctx context.Context, dest string, storage attachments.Storage, walker attachments.Walker, stdout io.Writer) error {
	if err := ensureDirNoSymlink(dest); err != nil {
		return err
	}
	unlock, err := lockMirrorDir(dest)
	if err != nil {
		return err
	}
	defer unlock()

	objRoot := filepath.Join(dest, objectsDir)
	if err := ensureDirNoSymlink(objRoot); err != nil {
		return err
	}
	if err := cleanAndValidateMirrorTree(dest); err != nil {
		return err
	}
	manifest, err := readMirrorManifest(dest)
	if err != nil {
		return err
	}
	if err := validateMirrorObjectSet(dest, manifest); err != nil {
		return err
	}
	for key, known := range manifest {
		target := filepath.Join(objRoot, filepath.FromSlash(key))
		if err := verifyMirroredFile(target, known); err != nil {
			return fmt.Errorf("objeto já espelhado %s corrompido ou ausente: %w", key, err)
		}
	}

	started := time.Now()
	var copied, skipped, refused int
	var copiedBytes int64
	err = walker.Walk(ctx, func(key, version string, size int64) error {
		if !objectKeyPattern.MatchString(key) {
			refused++
			return nil
		}
		target := filepath.Join(objRoot, filepath.FromSlash(key))
		if err := ensureDirNoSymlink(filepath.Dir(target)); err != nil {
			return err
		}
		if known, ok := manifest[key]; ok {
			if known.Version != version || known.Bytes != size {
				return fmt.Errorf("alteração inesperada do objeto já espelhado %s (versão %s/%d -> %s/%d); sobrescrita recusada", key, known.Version, known.Bytes, version, size)
			}
			if err := verifyMirroredFile(target, known); err != nil {
				return fmt.Errorf("objeto já espelhado %s corrompido ou ausente: %w", key, err)
			}
			skipped++
			return nil
		}
		if len(manifest) >= maxMirrorObjects {
			return errors.New("limite de objetos do espelho atingido")
		}
		if version == "" || strings.ContainsAny(version, "\t\r\n") || size < 0 || size > attachments.MaxFileBytes {
			return fmt.Errorf("metadados inválidos do objeto %s", key)
		}
		if info, err := os.Lstat(target); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("caminho com symlink recusado no espelho")
			}
			return fmt.Errorf("arquivo %s já existe no espelho fora do manifesto; sobrescrita recusada", key)
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("destino do espelho inacessível")
		}

		reader, err := storage.Read(ctx, key, version)
		if err != nil {
			return fmt.Errorf("leitura do objeto %s: %w", key, err)
		}
		defer reader.Close()
		tmp, err := os.CreateTemp(filepath.Dir(target), ".incoming-*")
		if err != nil {
			return errors.New("destino do espelho inacessível")
		}
		defer os.Remove(tmp.Name())
		sum, md := sha256.New(), md5.New()
		n, err := io.Copy(io.MultiWriter(tmp, sum, md), io.LimitReader(reader, attachments.MaxFileBytes+1))
		if err != nil || n > attachments.MaxFileBytes {
			tmp.Close()
			return fmt.Errorf("cópia do objeto %s falhou", key)
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return errors.New("destino do espelho inacessível")
		}
		if err := tmp.Close(); err != nil {
			return errors.New("destino do espelho inacessível")
		}
		if n != size {
			return fmt.Errorf("objeto %s: tamanho copiado %d difere do listado %d", key, n, size)
		}
		// Single-part uploads have the MD5 of the bytes as ETag: a mismatch is corruption in transit.
		if etag, ok := strings.CutPrefix(version, "etag:"); ok && !strings.Contains(etag, "-") && etag != hex.EncodeToString(md.Sum(nil)) {
			return fmt.Errorf("objeto %s: bytes copiados não conferem com o ETag", key)
		}
		if err := os.Chmod(tmp.Name(), 0o400); err != nil {
			return errors.New("destino do espelho inacessível")
		}
		// os.Link nunca sobrescreve um destino existente (falha com EEXIST).
		if err := os.Link(tmp.Name(), target); err != nil {
			return fmt.Errorf("preservação do objeto %s recusou sobrescrita: %w", key, err)
		}
		if err := syncMirrorDirectory(filepath.Dir(target)); err != nil {
			return err
		}
		manifest[key] = mirrorEntry{Key: key, Version: version, SHA: hex.EncodeToString(sum.Sum(nil)), Bytes: n}
		copied++
		copiedBytes += n
		return nil
	})
	if err != nil {
		return err
	}
	if err := writeMirrorManifest(dest, manifest); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "attachments-backup: %d objeto(s) copiado(s) (%d bytes), %d já espelhado(s), %d chave(s) fora do padrão recusada(s), %d no manifesto, em %s\n",
		copied, copiedBytes, skipped, refused, len(manifest), time.Since(started).Round(time.Millisecond))
	if refused > 0 {
		return fmt.Errorf("%d chave(s) do bucket não seguem o padrão namespace/objeto e não foram espelhadas", refused)
	}
	return nil
}

// attachmentsRestore writes the mirrored objects back into the configured
// bucket under the same keys, checks every byte against the manifest hash and
// refuses a result whose version identity differs from the manifest. Keys in
// -skip (attachments excluded after the backup) are not restored.
func attachmentsRestore(ctx context.Context, args, environ []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("attachments-restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	src := flags.String("src", "", "diretório do espelho")
	skip := flags.String("skip", "", "arquivo com uma chave de objeto por linha que não deve voltar")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *src == "" {
		return errors.New("uso: chatisp attachments-restore -src DIR [-skip ARQUIVO]")
	}
	storage, _, err := mirrorStorage(environ)
	if err != nil {
		return err
	}
	return attachmentsRestoreWith(ctx, *src, *skip, storage, stdout)
}

func attachmentsRestoreWith(ctx context.Context, src, skip string, storage attachments.Storage, stdout io.Writer) error {
	if err := checkNoSymlink(src); err != nil {
		return err
	}
	unlock, err := lockMirrorDir(src)
	if err != nil {
		return err
	}
	defer unlock()

	if err := cleanAndValidateMirrorTree(src); err != nil {
		return err
	}
	manifest, err := readMirrorManifest(src)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(src, manifestName)); err != nil {
		return errors.New("manifesto do espelho ausente")
	}
	if err := validateMirrorObjectSet(src, manifest); err != nil {
		return err
	}
	skipped, err := readKeySet(skip)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(manifest))
	for key := range manifest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Validate the complete source before the first network write. A truncated
	// or corrupt later object must not yield a partially restored bucket.
	for _, key := range keys {
		if err := verifyMirroredFile(filepath.Join(src, objectsDir, filepath.FromSlash(key)), manifest[key]); err != nil {
			return fmt.Errorf("objeto %s do espelho não confere com o manifesto: %w", key, err)
		}
	}
	var restored, present, excluded int
	for _, key := range keys {
		entry := manifest[key]
		if skipped[key] {
			excluded++
			continue
		}
		target := filepath.Join(src, objectsDir, filepath.FromSlash(key))
		if err := checkNoSymlink(filepath.Dir(target)); err != nil {
			return err
		}
		if err := checkNoSymlink(target); err != nil {
			return err
		}
		body, err := os.ReadFile(target)
		if err != nil {
			return fmt.Errorf("objeto %s ausente do espelho", key)
		}
		if got := sha256.Sum256(body); hex.EncodeToString(got[:]) != entry.SHA || int64(len(body)) != entry.Bytes {
			return fmt.Errorf("objeto %s do espelho não confere com o manifesto", key)
		}
		if reader, err := storage.Read(ctx, key, entry.Version); err == nil {
			existing, readErr := io.ReadAll(io.LimitReader(reader, attachments.MaxFileBytes+1))
			reader.Close()
			if readErr == nil && bytes.Equal(existing, body) {
				present++
				continue
			}
		}
		version, err := storage.Create(ctx, key, bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return fmt.Errorf("gravação do objeto %s no storage falhou", key)
		}
		if version != entry.Version {
			return fmt.Errorf("objeto %s voltou com identidade %s, esperado %s", key, version, entry.Version)
		}
		reader, err := storage.Read(ctx, key, entry.Version)
		if err != nil {
			return fmt.Errorf("objeto %s indisponível após restauração: %w", key, err)
		}
		readback, readErr := io.ReadAll(io.LimitReader(reader, attachments.MaxFileBytes+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(readback, body) {
			return fmt.Errorf("objeto %s restaurado não confere com o manifesto", key)
		}
		restored++
	}
	fmt.Fprintf(stdout, "attachments-restore: %d objeto(s) restaurado(s), %d já presente(s), %d excluído(s) pela política (não restaurados)\n", restored, present, excluded)
	return nil
}

// Reject symlinks in every ancestor, including a symlink above the supplied
// mirror root. The mirror and its parent directories must be operator-owned;
// the lock coordinates cooperating backup/restore processes.
func checkNoSymlink(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("caminho ausente ou inacessível no espelho: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("caminho com symlink recusado no espelho")
		}
		if current == filepath.Dir(current) {
			return nil
		}
	}
}

func ensureDirNoSymlink(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(absolute); err == nil {
		if err := checkNoSymlink(absolute); err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("destino do espelho inválido")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("destino do espelho inacessível")
	}
	if err := ensureDirNoSymlink(filepath.Dir(absolute)); err != nil {
		return err
	}
	if err := os.Mkdir(absolute, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return checkNoSymlink(absolute)
}

func syncMirrorDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateMirrorObjectSet(dir string, manifest map[string]mirrorEntry) error {
	root := filepath.Join(dir, objectsDir)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) && len(manifest) == 0 {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if _, ok := manifest[key]; !ok {
			return fmt.Errorf("espelho incompleto: arquivo %s fora do manifesto; sobrescrita recusada (preserve-o para recuperação manual)", key)
		}
		return nil
	})
}

func lockMirrorDir(dir string) (func(), error) {
	lockPath := filepath.Join(dir, lockName)
	if info, err := os.Lstat(lockPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("caminho com symlink recusado no espelho")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, errors.New("destino do espelho inacessível")
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("lock do espelho não é arquivo regular")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("outra operação já está em execução no espelho")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func cleanAndValidateMirrorTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.New("destino do espelho inacessível")
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(full)
		if err != nil {
			return errors.New("destino do espelho inacessível")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("caminho com symlink recusado no espelho")
		}
		if strings.HasPrefix(entry.Name(), ".manifest-") {
			if err := os.Remove(full); err != nil {
				return err
			}
		}
	}
	objRoot := filepath.Join(dir, objectsDir)
	if _, err := os.Lstat(objRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(objRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("caminho com symlink recusado no espelho")
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), ".incoming-") {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		return nil
	})
}

func verifyMirroredFile(path string, expected mirrorEntry) error {
	if err := checkNoSymlink(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("caminho com symlink recusado no espelho")
	}
	if !info.Mode().IsRegular() {
		return errors.New("objeto espelhado não é arquivo regular")
	}
	if info.Size() != expected.Bytes {
		return fmt.Errorf("tamanho %d difere do manifesto %d", info.Size(), expected.Bytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != expected.SHA {
		return fmt.Errorf("hash %s difere do manifesto %s", got, expected.SHA)
	}
	return nil
}

func readKeySet(path string) (map[string]bool, error) {
	set := map[string]bool{}
	if path == "" {
		return set, nil
	}
	if err := checkNoSymlink(path); err != nil {
		return nil, errors.New("arquivo de chaves excluídas inacessível")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("arquivo de chaves excluídas inacessível")
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key := strings.TrimSpace(scanner.Text())
		if key == "" {
			continue
		}
		if !objectKeyPattern.MatchString(key) {
			return nil, errors.New("arquivo de chaves excluídas com chave fora do padrão")
		}
		set[key] = true
	}
	return set, scanner.Err()
}

func readMirrorManifest(dir string) (map[string]mirrorEntry, error) {
	out := map[string]mirrorEntry{}
	manifestPath := filepath.Join(dir, manifestName)
	if _, err := os.Lstat(manifestPath); errors.Is(err, os.ErrNotExist) {
		return out, nil // Only a brand-new backup may have no manifest.
	}
	if err := checkNoSymlink(manifestPath); err != nil {
		return nil, err
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, errors.New("manifesto do espelho inacessível")
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	if !scanner.Scan() || scanner.Text() != manifestHeader {
		return nil, errors.New("manifesto do espelho com formato desconhecido ou truncado")
	}
	sum := sha256.New()
	complete := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, manifestFooter) {
			expected := fmt.Sprintf("%s entries=%d sha256=%s", manifestFooter, len(out), hex.EncodeToString(sum.Sum(nil)))
			if line != expected || scanner.Scan() {
				return nil, errors.New("manifesto do espelho corrompido ou com registros excedentes")
			}
			complete = true
			break
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || !objectKeyPattern.MatchString(fields[0]) || fields[1] == "" || strings.ContainsAny(fields[1], "\r\n") || len(fields[2]) != 64 {
			return nil, errors.New("manifesto do espelho corrompido")
		}
		if _, err := hex.DecodeString(fields[2]); err != nil {
			return nil, errors.New("hash inválido no manifesto do espelho")
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 || size > attachments.MaxFileBytes {
			return nil, errors.New("manifesto do espelho corrompido")
		}
		if _, duplicate := out[fields[0]]; duplicate || len(out) >= maxMirrorObjects {
			return nil, errors.New("manifesto do espelho com chave duplicada ou limite excedido")
		}
		out[fields[0]] = mirrorEntry{Key: fields[0], Version: fields[1], SHA: fields[2], Bytes: size}
		_, _ = io.WriteString(sum, line+"\n")
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !complete {
		return nil, errors.New("manifesto do espelho truncado: rodapé de integridade ausente")
	}
	return out, nil
}

func writeMirrorManifest(dir string, manifest map[string]mirrorEntry) error {
	manifestPath := filepath.Join(dir, manifestName)
	if info, err := os.Lstat(manifestPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("caminho com symlink recusado no espelho")
	}
	keys := make([]string, 0, len(manifest))
	for key := range manifest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(manifestHeader + "\n")
	sum := sha256.New()
	for _, key := range keys {
		e := manifest[key]
		line := fmt.Sprintf("%s\t%s\t%s\t%d\n", e.Key, e.Version, e.SHA, e.Bytes)
		b.WriteString(line)
		_, _ = io.WriteString(sum, line)
	}
	fmt.Fprintf(&b, "%s entries=%d sha256=%s\n", manifestFooter, len(keys), hex.EncodeToString(sum.Sum(nil)))
	tmp, err := os.CreateTemp(dir, ".manifest-*")
	if err != nil {
		return errors.New("manifesto do espelho inacessível")
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return errors.New("manifesto do espelho inacessível")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return errors.New("manifesto do espelho inacessível")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("manifesto do espelho inacessível")
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return errors.New("manifesto do espelho inacessível")
	}
	if err := os.Rename(tmp.Name(), manifestPath); err != nil {
		return err
	}
	return syncMirrorDirectory(dir)
}
