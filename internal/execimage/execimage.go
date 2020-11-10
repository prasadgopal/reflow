// Copyright 2018 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package execimage

import (
	"crypto"
	_ "crypto/sha256" // Needed for crypto.SHA256
	"debug/elf"
	"debug/macho"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/grailbio/base/digest"
	"github.com/grailbio/base/sync/once"
	"github.com/grailbio/reflow/errors"
)

var (
	digester     = digest.Digester(crypto.SHA256)
	binaryDigest digest.Digest
	digestOnce   once.Task
)

// ExecPath returns an absolute path to the executable of the current running process.
func ExecPath() (string, error) {
	// TODO(marius): use /proc/self/exe on Linux
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("evalsymlinks: %s %v", path, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute: %s %v", path, err)
	}
	return path, nil
}

// ImageDigest returns the digest of the executable of the current running process.
func ImageDigest() (digest.Digest, error) {
	err := digestOnce.Do(func() error {
		var err error
		path, err := ExecPath()
		if err != nil {
			return err
		}
		r, err := os.Open(path)
		if err != nil {
			return err
		}
		defer r.Close()
		binaryDigest, err = Digest(r)
		return err
	})
	return binaryDigest, err
}

// Digest returns the digest of the given ReadCloser and closes it.
func Digest(r io.Reader) (digest.Digest, error) {
	var dig digest.Digest
	dw := digester.NewWriter()
	if _, err := io.Copy(dw, r); err != nil {
		return dig, err
	}
	dig = dw.Digest()
	return dig, nil
}

// InstallImageReflowlet reads a new image from its argument and replaces the current
// process with it. As a consequence, all state held by the caller is lost
// (pending requests, if any, etc) so its up to the caller to manage this interaction.
// TODO(dnicolaou) remove InstallImageReflowlet once the old reflowlet bootstrap
//  containing the reflow binary is replaced
func InstallImageReflowlet(exec io.ReadCloser, prefix string) error {
	f, err := ioutil.TempFile("", prefix)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, exec); err != nil {
		return err
	}
	if err := exec.Close(); err != nil {
		return err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, 0755); err != nil {
		return err
	}
	args := append([]string{}, os.Args...)
	args[0] = path
	log.Printf("exec %s", strings.Join(args, " "))
	err = syscall.Exec(path, args, os.Environ())
	return err
}

func sectionEndAligned(s *elf.Section) uint64 {
	return ((s.Offset + s.FileSize) + (s.Addralign - 1)) & -s.Addralign
}

// ErrNoEmbeddedImage is thrown if the current binary has no embedded linux image.
var ErrNoEmbeddedImage = errors.New("no embedded linux image")

// EmbeddedLinuxImage returns a reader pointing to an embedded linux image
// with the following assumptions:
// - if the current GOOS is linux, returns the current binary.
// - if the current GOOS is darwin, and current binary size is larger
// than what Mach-O reports, returns a reader to the current binary
// offset by the size of the darwin binary.
// - returns ErrNoEmbeddedImage if
//   - if the current GOOS is not darwin
//   - if the current GOOS is darwin, but there's no embedding.
func EmbeddedLinuxImage() (io.ReadCloser, error) {
	path, err := ExecPath()
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "linux" {
		elff, err := elf.Open(path)
		if err != nil {
			return nil, err
		}
		// We could embed a reflowlet in other binaries. This requires us to inspect the binary
		// and find out where the bits of the reflowlet binary start.
		// We read the ELF file sections and determine the section that comes last in the file.
		// The last section's end offset (aligned to the section's alignment) gives us the
		// size of the file.
		var lastOffset uint64
		for _, s := range elff.Sections {
			// section type SHT_NOBITS occupies no space in the file.
			switch {
			case s.Type == elf.SHT_NOBITS:
				continue
			case lastOffset == 0:
				lastOffset = sectionEndAligned(s)
			default:
				offset := sectionEndAligned(s)
				if offset > lastOffset {
					lastOffset = offset
				}
			}
		}
		if lastOffset > uint64(fi.Size()) {
			return nil, errors.New(fmt.Sprintf("ELF file computed size greater than actual size (%v vs %v)", lastOffset, fi.Size()))
		}
		if lastOffset == uint64(fi.Size()) {
			return os.Open(path)
		}
		r, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		if _, err = r.Seek(int64(lastOffset), io.SeekStart); err != nil {
			return nil, err
		}
		return r, nil
	}
	fh, err := macho.Open(path)
	if err != nil {
		return nil, fmt.Errorf("unsupported binary, not mach-o: %v", err)
	}
	sg := fh.Segment("__LINKEDIT")
	machoSize := int64(sg.SegmentHeader.Filesz + sg.SegmentHeader.Offset)
	if fi.Size() == machoSize {
		return nil, ErrNoEmbeddedImage
	}
	r, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err = r.Seek(machoSize, io.SeekStart); err != nil {
		return nil, err
	}
	return r, nil
}
