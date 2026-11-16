package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/sys/unix"
)

const defaultPackageDir = "/usr/share/chatisp/packages"

var packageFiles = map[string]string{"inventario-funcional": "CHATISP_PACOTE_FUNCIONAL_PARA_DESIGN.zip", "briefing-tecnico": "CHATISP_BRIEFING_TECNICO_E_FRONTEND.zip", "referencias-visuais": "CHATISP_REFERENCIAS_VISUAIS.zip"}
var packageHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

type packageMetadata struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}
type packagesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         struct {
		Packages []packageMetadata `json:"packages"`
	}
}
type packageInput struct {
	Session string `cookie:"__Host-session"`
	ID      string `path:"id"`
}

func registerPackages(api huma.API, deps Dependencies) {
	panel := &panelAPI{auth: deps.Auth}
	authorize := func(ctx context.Context, cookie string) error {
		principal, err := panel.readPanel(ctx, cookie, "", false)
		if err != nil {
			return err
		}
		if principal.Role != "admin" {
			return huma.Error403Forbidden("acesso negado")
		}
		return nil
	}
	dir := deps.PackageDir
	if dir == "" {
		dir = defaultPackageDir
	}
	huma.Register(api, panelOp("list-panel-packages", http.MethodGet, "/api/panel/packages", "Lista pacotes", "Somente administradores.", http.StatusOK, []int{401, 403, 503}), func(ctx context.Context, in *cookieInput) (*packagesOutput, error) {
		if err := authorize(ctx, in.Session); err != nil {
			return nil, err
		}
		packages, err := readPackageManifest(dir)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("pacotes indisponíveis")
		}
		out := &packagesOutput{CacheControl: "private, no-store"}
		out.Body.Packages = packages
		return out, nil
	})
	huma.Register(api, panelOp("download-panel-package", http.MethodGet, "/api/panel/packages/{id}/download", "Baixa pacote", "Somente administradores.", http.StatusOK, []int{401, 403, 404, 503}), func(ctx context.Context, in *packageInput) (*huma.StreamResponse, error) {
		if err := authorize(ctx, in.Session); err != nil {
			return nil, err
		}
		if _, ok := packageFiles[in.ID]; !ok {
			return nil, huma.Error404NotFound("pacote não encontrado")
		}
		packages, err := readPackageManifest(dir)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable("pacotes indisponíveis")
		}
		for _, p := range packages {
			if p.ID == in.ID {
				f, err := openPackageFile(dir, p.Filename)
				if err != nil {
					return nil, huma.Error503ServiceUnavailable("pacote indisponível")
				}
				stat, err := f.Stat()
				if err != nil || stat.Size() != p.Size {
					f.Close()
					return nil, huma.Error503ServiceUnavailable("pacote indisponível")
				}
				hash := sha256.New()
				if _, err := io.Copy(hash, f); err != nil || hex.EncodeToString(hash.Sum(nil)) != p.SHA256 {
					f.Close()
					return nil, huma.Error503ServiceUnavailable("pacote indisponível")
				}
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					f.Close()
					return nil, huma.Error503ServiceUnavailable("pacote indisponível")
				}
				return &huma.StreamResponse{Body: func(c huma.Context) {
					defer f.Close()
					c.SetHeader("Content-Type", "application/zip")
					c.SetHeader("Content-Disposition", `attachment; filename="`+p.Filename+`"`)
					c.SetHeader("Content-Length", strconv.FormatInt(p.Size, 10))
					c.SetHeader("X-Content-Type-Options", "nosniff")
					c.SetHeader("Cache-Control", "private, no-store")
					_, _ = io.Copy(c.BodyWriter(), f)
				}}, nil
			}
		}
		return nil, huma.Error404NotFound("pacote não encontrado")
	})
}

func openPackageFile(dir, name string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(dir, name), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("invalid package file")
	}
	return f, nil
}

func readPackageManifest(dir string) ([]packageMetadata, error) {
	f, err := openPackageFile(dir, "manifest.json")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var manifest struct {
		Packages []packageMetadata `json:"packages"`
	}
	decoder := json.NewDecoder(io.LimitReader(f, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid manifest")
	}
	seen := map[string]bool{}
	for _, p := range manifest.Packages {
		name, ok := packageFiles[p.ID]
		if !ok || seen[p.ID] || p.Filename != name || p.Title == "" || p.Description == "" || p.Size <= 0 || !packageHash.MatchString(p.SHA256) {
			return nil, errors.New("invalid package metadata")
		}
		seen[p.ID] = true
	}
	if manifest.Packages == nil {
		return nil, errors.New("invalid manifest")
	}
	return manifest.Packages, nil
}
