package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const DefaultCommand = "vipsthumbnail"

type Thumbnailer struct {
	Dir     string
	Command string
}

func NewThumbnailer(dir string) *Thumbnailer {
	return &Thumbnailer{Dir: dir, Command: DefaultCommand}
}

// MaxSize is the long-edge target for thumbnails.
const MaxSize = 512

// Generate produces {Dir}/{key}.webp from source, written first to a temp
// file in Dir and atomically renamed. key must be a plain basename.
//
// srcMaxDim is the caller's best knowledge of the source image's longer
// dimension (0 means "unknown, assume large"). It is used only to clamp the
// target size and prevent upscaling — vipsthumbnail 8.14 has no built-in
// no-upscale flag, so we simply never ask it to shrink below source size.
func (t *Thumbnailer) Generate(ctx context.Context, source, key string, srcMaxDim int) error {
	if key == "" || filepath.Base(key) != key || key == "." || key == ".." {
		return fmt.Errorf("invalid thumbnail key %q", key)
	}
	if err := os.MkdirAll(t.Dir, 0o750); err != nil {
		return fmt.Errorf("mkdir thumbnail dir: %w", err)
	}
	tmp, err := os.CreateTemp(t.Dir, ".thumb-*.webp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	// Let vipsthumbnail create the file fresh at tmpPath.
	_ = os.Remove(tmpPath)

	size := MaxSize
	if srcMaxDim > 0 && srcMaxDim < size {
		size = srcMaxDim
	}

	final := filepath.Join(t.Dir, key+".webp")
	args := []string{
		source,
		"--size", fmt.Sprintf("%dx%d", size, size),
		"--output", tmpPath + "[Q=80,strip]",
	}
	cmd := exec.CommandContext(ctx, t.Command, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("vipsthumbnail: %w: %s", err, string(out))
	}
	if _, err := os.Stat(tmpPath); err != nil {
		return fmt.Errorf("thumbnail temp missing after command: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename thumbnail: %w", err)
	}
	return nil
}
