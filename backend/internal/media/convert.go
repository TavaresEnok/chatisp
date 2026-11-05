package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"chatisp/internal/attachments"
	"chatisp/internal/platform/auth"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	convertTimeout  = 20 * time.Second
	convertDuration = 120 * time.Second
	convertOutput   = 8 << 20
)

// Convert asks for a local OGG/Opus copy. Without ffmpeg the call is refused,
// the original attachment bytes stay in storage, and the row records the refusal.
// A successful run publishes a second validated attachment and still keeps the original.
func (s *Service) Convert(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID) (MediaItem, error) {
	var out MediaItem
	var original []byte
	var row storedAttachment
	missing := false
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		item, e := loadItem(c, tx, p.TenantID, conv, id)
		if e != nil {
			return e
		}
		out = item
		if item.ConversionState == "converted" {
			return ErrConflict
		}
		src, e := loadAttachment(c, tx, p.TenantID, conv, item.AttachmentID)
		if e != nil {
			return e
		}
		if src.state != "validated" {
			return ErrConflict
		}
		original, e = readExact(c, s.Attachments.Storage, src)
		if e != nil {
			return e
		}
		row = src
		if limErr := recordedWithinLimits(row.mediaType, original); limErr != nil {
			if e = markRefused(c, tx, p.TenantID, id, FFmpegFailedDetail); e != nil {
				return e
			}
			return ErrConvert
		}
		if _, lookErr := s.look(); lookErr != nil {
			missing = true
			if item.ConversionState == "refused" && item.ConversionError == FFmpegMissingDetail {
				return ErrFFmpegMissing
			}
			if e = markRefused(c, tx, p.TenantID, id, FFmpegMissingDetail); e != nil {
				return e
			}
			return ErrFFmpegMissing
		}
		return nil
	})
	if missing || errors.Is(e, ErrFFmpegMissing) {
		if e == nil {
			e = ErrFFmpegMissing
		}
		if out.ID != uuid.Nil {
			out.ConversionState = "refused"
			out.ConversionError = FFmpegMissingDetail
			out.OutputAttachmentID = nil
		}
		return out, ErrFFmpegMissing
	}
	if errors.Is(e, ErrConvert) {
		out.ConversionState = "refused"
		out.ConversionError = FFmpegFailedDetail
		out.OutputAttachmentID = nil
		return out, ErrConvert
	}
	if e != nil {
		return out, e
	}
	produced, runErr := s.transcode(ctx, original)
	if runErr != nil || len(produced) == 0 || len(produced) > convertOutput {
		return s.refuse(ctx, p, inbox, conv, id, out, FFmpegFailedDetail, ErrConvert)
	}
	rec, e := s.Attachments.Register(ctx, p, inbox, conv, attachments.RegisterInput{
		RequestID: uuid.New(), Name: "audio.ogg", Bytes: int64(len(produced)), Direction: row.direction,
	})
	if e != nil {
		return s.refuse(ctx, p, inbox, conv, id, out, FFmpegFailedDetail, e)
	}
	if e = s.Attachments.Upload(ctx, p, inbox, conv, rec.ID, bytesReader(produced)); e != nil {
		return s.refuse(ctx, p, inbox, conv, id, out, FFmpegFailedDetail, e)
	}
	validated, e := s.Attachments.Validate(ctx, p, inbox, conv, rec.ID)
	if e != nil || validated.State != "validated" || validated.MediaType == "" {
		if e == nil {
			e = ErrConvert
		}
		return s.refuse(ctx, p, inbox, conv, id, out, FFmpegFailedDetail, e)
	}
	e = s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		current, e := loadItem(c, tx, p.TenantID, conv, id)
		if e != nil {
			return e
		}
		if current.ConversionState == "converted" {
			return ErrConflict
		}
		again, e := readExact(c, s.Attachments.Storage, row)
		if e != nil {
			return e
		}
		if !bytes.Equal(again, original) {
			return ErrConflict
		}
		tag, e := tx.Exec(c, `
			UPDATE media_items
			SET conversion_state='converted', conversion_error='', output_attachment_id=$4
			WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3 AND conversion_state<>'converted'`,
			p.TenantID, conv, id, rec.ID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		out = current
		out.ConversionState = "converted"
		out.ConversionError = ""
		published := rec.ID
		out.OutputAttachmentID = &published
		return nil
	})
	if e != nil {
		return out, e
	}
	return out, nil
}

func (s *Service) refuse(ctx context.Context, p auth.Principal, inbox, conv, id uuid.UUID, current MediaItem, detail string, cause error) (MediaItem, error) {
	e := s.run(ctx, p, inbox, conv, func(c context.Context, tx pgx.Tx) error {
		item, loadErr := loadItem(c, tx, p.TenantID, conv, id)
		if loadErr != nil || item.ConversionState == "converted" {
			return loadErr
		}
		return markRefused(c, tx, p.TenantID, id, detail)
	})
	if e != nil {
		return current, e
	}
	current.ConversionState = "refused"
	current.ConversionError = detail
	current.OutputAttachmentID = nil
	return current, cause
}

func (s *Service) look() (string, error) {
	if s.LookFFmpeg != nil {
		return s.LookFFmpeg()
	}
	return exec.LookPath("ffmpeg")
}

func (s *Service) transcode(ctx context.Context, original []byte) ([]byte, error) {
	bin, e := s.look()
	if e != nil {
		return nil, ErrFFmpegMissing
	}
	run := s.RunFFmpeg
	if run == nil {
		run = defaultRun
	}
	return runLocalTranscode(ctx, bin, run, original)
}

// LookBinary resolves the local ffmpeg. A missing binary is the same refusal
// the convert endpoint already stores.
func LookBinary() (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", ErrFFmpegMissing
	}
	return path, nil
}

// TranscodeRecorded converts one recorded webm/ogg to OGG/Opus.
// A declared duration above 120s, or a size above the limit, is refused before
// the binary is resolved. A declaration inside the limit is not trusted: OGG
// and WebM are measured with a local ffmpeg decode even when Duration is
// present, and a decoded length above 120s is refused before the output is
// accepted. Truncated output is not success. A small file without the binary
// returns ErrFFmpegMissing and the caller keeps the original bytes.
func TranscodeRecorded(ctx context.Context, mime string, original []byte) ([]byte, error) {
	if err := recordedWithinLimits(mime, original); err != nil {
		return nil, err
	}
	_, hasDeclared := ContainerDuration(mime, original)
	bin, err := LookBinary()
	if err != nil {
		return nil, err
	}
	measure := hasDeclared || needsEffectiveDuration(mime)
	var inputMS int64
	if measure {
		inputMS, err = probeBytesDuration(ctx, bin, original)
		if err != nil || inputMS <= 0 || inputMS > convertDuration.Milliseconds() {
			return nil, ErrConvert
		}
	}
	produced, err := runLocalTranscode(ctx, bin, defaultRun, original)
	if err != nil {
		return nil, err
	}
	if measure {
		outMS, err := probeBytesDuration(ctx, bin, produced)
		if err != nil || outMS <= 0 {
			return nil, ErrConvert
		}
		if inputMS > 0 && inputMS-outMS > 2000 {
			return nil, ErrConvert
		}
		if outMS > convertDuration.Milliseconds()+2000 {
			return nil, ErrConvert
		}
	}
	return produced, nil
}

func needsEffectiveDuration(mime string) bool {
	switch strings.ToLower(mime) {
	case "audio/webm", "video/webm", "audio/ogg", "application/ogg", "video/ogg",
		"audio/mpeg", "audio/mp3", "audio/mp4", "audio/m4a", "audio/x-m4a",
		"audio/wav", "audio/wave", "audio/x-wav":
		return true
	default:
		return false
	}
}

func probeBytesDuration(ctx context.Context, bin string, body []byte) (int64, error) {
	dir, err := os.MkdirTemp(os.TempDir(), "chatisp-media-")
	if err != nil {
		return 0, ErrConvert
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "entrada.bin")
	if err = os.WriteFile(in, body, 0o600); err != nil {
		return 0, ErrConvert
	}
	args, err := probeFFmpegArgs(in)
	if err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()
	log, runErr := probeRun(cctx, bin, args)
	ms, ok := ParseProbeDuration(log)
	if !ok || ms <= 0 {
		return 0, ErrConvert
	}
	if runErr != nil && cctx.Err() != nil {
		return 0, ErrConvert
	}
	return ms, nil
}

func probeFFmpegArgs(in string) ([]string, error) {
	if strings.Contains(in, "://") {
		return nil, ErrInvalid
	}
	inAbs, err := filepath.Abs(in)
	if err != nil {
		return nil, ErrInvalid
	}
	sink := filepath.Join(filepath.Dir(inAbs), "nulo")
	return []string{
		"-hide_banner", "-nostdin",
		"-protocol_whitelist", "file,crypto",
		"-i", inAbs,
		"-f", "null", sink,
	}, nil
}

var probeClock = regexp.MustCompile(`(?:Duration: |time=)(\d+):(\d{2}):(\d{2})\.(\d+)`)

// ParseProbeDuration reads the longest clock ffmpeg printed. Duration: N/A is
// ignored. The value is milliseconds.
func ParseProbeDuration(log string) (int64, bool) {
	var best int64
	found := false
	for _, match := range probeClock.FindAllStringSubmatch(log, -1) {
		ms, ok := clockMillis(match[1], match[2], match[3], match[4])
		if !ok {
			continue
		}
		if !found || ms > best {
			best = ms
			found = true
		}
	}
	return best, found
}

func clockMillis(hour, minute, second, frac string) (int64, bool) {
	h, err1 := strconv.ParseInt(hour, 10, 64)
	m, err2 := strconv.ParseInt(minute, 10, 64)
	s, err3 := strconv.ParseInt(second, 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, false
	}
	if len(frac) > 3 {
		frac = frac[:3]
	}
	for len(frac) < 3 {
		frac += "0"
	}
	ms, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return ((h*60+m)*60+s)*1000 + ms, true
}

func probeRun(ctx context.Context, bin string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = nil
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir()}
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &limitedBuffer{buf: &stderr, max: 1 << 20}
	err := cmd.Run()
	return stderr.String(), err
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room > 0 {
		if len(p) > room {
			_, _ = w.buf.Write(p[:room])
		} else {
			_, _ = w.buf.Write(p)
		}
	}
	return len(p), nil
}

func recordedWithinLimits(mime string, original []byte) error {
	if len(original) == 0 || len(original) > convertOutput {
		return ErrConvert
	}
	if ms, ok := ContainerDuration(mime, original); ok && ms > convertDuration.Milliseconds() {
		return ErrConvert
	}
	return nil
}

func runLocalTranscode(ctx context.Context, bin string, run func(context.Context, string, []string) error, original []byte) ([]byte, error) {
	if len(original) == 0 || len(original) > convertOutput {
		return nil, ErrConvert
	}
	dir, e := os.MkdirTemp(os.TempDir(), "chatisp-media-")
	if e != nil {
		return nil, ErrConvert
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "entrada.bin")
	out := filepath.Join(dir, "saida.ogg")
	if e = os.WriteFile(in, original, 0o600); e != nil {
		return nil, ErrConvert
	}
	args, e := localFFmpegArgs(in, out)
	if e != nil {
		return nil, e
	}
	cctx, cancel := context.WithTimeout(ctx, convertTimeout)
	defer cancel()
	if e = run(cctx, bin, args); e != nil {
		return nil, ErrConvert
	}
	f, e := os.Open(out)
	if e != nil {
		return nil, ErrConvert
	}
	defer f.Close()
	produced, e := io.ReadAll(io.LimitReader(f, convertOutput+1))
	if e != nil || len(produced) == 0 || len(produced) > convertOutput {
		return nil, ErrConvert
	}
	return produced, nil
}

func localFFmpegArgs(in, out string) ([]string, error) {
	if strings.Contains(in, "://") || strings.Contains(out, "://") {
		return nil, ErrInvalid
	}
	inAbs, e := filepath.Abs(in)
	if e != nil {
		return nil, ErrInvalid
	}
	outAbs, e := filepath.Abs(out)
	if e != nil {
		return nil, ErrInvalid
	}
	root, e := filepath.Abs(filepath.Dir(inAbs))
	if e != nil || filepath.Dir(outAbs) != root {
		return nil, ErrInvalid
	}
	return []string{
		"-hide_banner", "-nostdin", "-y",
		"-protocol_whitelist", "file,crypto",
		"-i", inAbs,
		"-t", strconv.FormatFloat(convertDuration.Seconds(), 'f', 0, 64),
		"-fs", strconv.FormatInt(convertOutput, 10),
		"-vn", "-ac", "1", "-c:a", "libopus", "-f", "ogg",
		outAbs,
	}, nil
}

// Resource ceilings applied to the ffmpeg child through prlimit (util-linux):
// CPU seconds, address space, largest file written and open descriptors. They are
// a process-level cap, not a cgroup; a host without prlimit runs ffmpeg with the
// existing timeout, duration and size checks only.
const (
	ffmpegCPUSeconds = 30
	ffmpegAddress    = 2 << 30
	ffmpegFileBytes  = 16 << 20
	ffmpegOpenFiles  = 64
)

func limitedCommand(bin string, args []string) (string, []string) {
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		return bin, args
	}
	wrapped := []string{
		fmt.Sprintf("--cpu=%d", ffmpegCPUSeconds),
		fmt.Sprintf("--as=%d", ffmpegAddress),
		fmt.Sprintf("--fsize=%d", ffmpegFileBytes),
		fmt.Sprintf("--nofile=%d", ffmpegOpenFiles),
		"--", bin,
	}
	return prlimit, append(wrapped, args...)
}

func defaultRun(ctx context.Context, bin string, args []string) error {
	name, argv := limitedCommand(bin, args)
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir()}
	return cmd.Run()
}

func markRefused(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, detail string) error {
	tag, e := tx.Exec(ctx, `
		UPDATE media_items
		SET conversion_state='refused', conversion_error=$3, output_attachment_id=NULL
		WHERE tenant_id=$1 AND id=$2 AND conversion_state<>'converted'`, tenant, id, detail)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
