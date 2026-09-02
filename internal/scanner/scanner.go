package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Entry struct {
	CategoryName, CategoryPath string
	AlbumName, AlbumPath       string
	Filename, RelativePath     string
	Size, MTimeNS              int64
}

type Warning struct {
	Path, Code string
}

// Warning codes emitted by Walk.
const (
	CodeRootFile     = "root_file"
	CodeCategoryFile = "category_file"
	CodeTooDeep      = "too_deep"
	CodeUnsupported  = "unsupported"
	CodeSymlink      = "symlink"
	CodeUnreadable   = "unreadable"
)

var supportedExt = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".webp": {},
}

type readDirFn func(string) ([]os.DirEntry, error)

func Walk(root string, warn func(Warning)) ([]Entry, error) {
	return walkWith(root, warn, os.ReadDir)
}

func walkWith(root string, warn func(Warning), readDir readDirFn) ([]Entry, error) {
	if warn == nil {
		warn = func(Warning) {}
	}
	categories, err := readDir(root)
	if err != nil {
		return nil, fmt.Errorf("read root %q: %w", root, err)
	}
	var out []Entry
	for _, category := range categories {
		if isHidden(category.Name()) {
			continue
		}
		relCategory := category.Name()
		absCategory := filepath.Join(root, category.Name())
		if category.Type()&os.ModeSymlink != 0 {
			warn(Warning{Path: relCategory, Code: CodeSymlink})
			continue
		}
		if !category.IsDir() {
			warn(Warning{Path: relCategory, Code: CodeRootFile})
			continue
		}
		albums, err := readDir(absCategory)
		if err != nil {
			return nil, fmt.Errorf("read category %q: %w", relCategory, err)
		}
		for _, album := range albums {
			if isHidden(album.Name()) {
				continue
			}
			relAlbum := filepath.ToSlash(filepath.Join(relCategory, album.Name()))
			absAlbum := filepath.Join(absCategory, album.Name())
			if album.Type()&os.ModeSymlink != 0 {
				warn(Warning{Path: relAlbum, Code: CodeSymlink})
				continue
			}
			if !album.IsDir() {
				warn(Warning{Path: relAlbum, Code: CodeCategoryFile})
				continue
			}
			photos, err := readDir(absAlbum)
			if err != nil {
				return nil, fmt.Errorf("read album %q: %w", relAlbum, err)
			}
			for _, photo := range photos {
				if isHidden(photo.Name()) {
					continue
				}
				relPhoto := filepath.ToSlash(filepath.Join(relAlbum, photo.Name()))
				if photo.Type()&os.ModeSymlink != 0 {
					warn(Warning{Path: relPhoto, Code: CodeSymlink})
					continue
				}
				if photo.IsDir() {
					warn(Warning{Path: relPhoto, Code: CodeTooDeep})
					continue
				}
				if !isSupported(photo.Name()) {
					warn(Warning{Path: relPhoto, Code: CodeUnsupported})
					continue
				}
				info, err := photo.Info()
				if err != nil {
					warn(Warning{Path: relPhoto, Code: CodeUnreadable})
					continue
				}
				out = append(out, Entry{
					CategoryName: category.Name(),
					CategoryPath: relCategory,
					AlbumName:    album.Name(),
					AlbumPath:    relAlbum,
					Filename:     photo.Name(),
					RelativePath: relPhoto,
					Size:         info.Size(),
					MTimeNS:      info.ModTime().UnixNano(),
				})
			}
		}
	}
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.RelativePath, b.RelativePath) })
	return out, nil
}

func isHidden(name string) bool { return strings.HasPrefix(name, ".") }

func isSupported(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := supportedExt[ext]
	return ok
}
