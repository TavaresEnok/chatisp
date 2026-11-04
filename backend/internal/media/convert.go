package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		src, e := loadAttachment(c, tx, p.TenantID, conv, item.AttachmentID)
		if e != nil {
			return e
		}
		if src.state != "validated" {
			return ErrConflict
		}
		original, e = readExact(c, s.Attachments.Storage, src)
		row = src
		return e
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
	run := s.RunFFmpeg
	if run == nil {
		run = defaultRun
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

func defaultRun(ctx context.Context, bin string, args []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
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
