package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// binaryPath is where the image has the storycreep binary.
const binaryPath = "usr/local/bin/storycreep"

// descriptor is an OCI content descriptor.
type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}

// imageConfig is the part of an image's configuration that the tests read.
type imageConfig struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Config       struct {
		User         string              `json:"User"`
		Entrypoint   []string            `json:"Entrypoint"`
		Cmd          []string            `json:"Cmd"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Healthcheck  *struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
		Labels map[string]string `json:"Labels"`
	} `json:"config"`
}

// fileEntry is a file of an image's filesystem, after its layers are applied.
type fileEntry struct {
	typeflag byte
	mode     int64
	uid, gid int
	// elf is set for a regular file that is an ELF executable or library.
	elf bool
}

// image is one platform's image of the archive.
type image struct {
	platform string
	config   imageConfig
	files    map[string]fileEntry
	// binary is the content of the storycreep binary.
	binary []byte
}

// archive returns the images of the OCI archive that
// STORYCREEP_TEST_IMAGE_ARCHIVE names, read once for every test, or skips the
// test if it names none.
func archive(t *testing.T) []image {
	t.Helper()
	p := os.Getenv("STORYCREEP_TEST_IMAGE_ARCHIVE")
	if p == "" {
		t.Skip("STORYCREEP_TEST_IMAGE_ARCHIVE names no image archive; CI's image job sets it")
	}
	images, err := readArchive()
	if err != nil {
		t.Fatal(err)
	}
	return images
}

var readArchive = sync.OnceValues(func() ([]image, error) {
	return loadArchive(os.Getenv("STORYCREEP_TEST_IMAGE_ARCHIVE"))
})

// loadArchive reads the OCI image layout in the tar archive at p, and returns
// its platforms' images. It leaves out the attestation manifests, which
// BuildKit adds to the index with the platform unknown/unknown.
func loadArchive(p string) ([]image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	files := map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		files[path.Clean(h.Name)] = b
	}
	blob := func(d descriptor) ([]byte, error) {
		alg, hex, ok := strings.Cut(d.Digest, ":")
		b, found := files[path.Join("blobs", alg, hex)]
		if !ok || !found {
			return nil, fmt.Errorf("%s: no blob %s", p, d.Digest)
		}
		return b, nil
	}

	var images []image
	var walk func(index []byte) error
	walk = func(index []byte) error {
		var idx struct {
			Manifests []descriptor `json:"manifests"`
		}
		if err := json.Unmarshal(index, &idx); err != nil {
			return err
		}
		for _, d := range idx.Manifests {
			b, err := blob(d)
			if err != nil {
				return err
			}
			switch d.MediaType {
			case "application/vnd.oci.image.index.v1+json",
				"application/vnd.docker.distribution.manifest.list.v2+json":
				if err := walk(b); err != nil {
					return err
				}
			case "application/vnd.oci.image.manifest.v1+json",
				"application/vnd.docker.distribution.manifest.v2+json":
				if d.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
					continue
				}
				img, err := readImage(b, blob)
				if err != nil {
					return fmt.Errorf("%s: the manifest %s: %w", p, d.Digest, err)
				}
				// An archive of one platform's image may give the manifest
				// no platform.
				img.platform = img.config.OS + "/" + img.config.Architecture
				if d.Platform != nil {
					img.platform = d.Platform.OS + "/" + d.Platform.Architecture
				}
				images = append(images, img)
			default:
				return fmt.Errorf("%s: %s has the unknown media type %q", p, d.Digest, d.MediaType)
			}
		}
		return nil
	}
	index, ok := files["index.json"]
	if !ok {
		return nil, fmt.Errorf("%s is not an OCI image layout: it has no index.json", p)
	}
	return images, walk(index)
}

// readImage reads the image of the manifest m, with its layers applied in
// order.
func readImage(m []byte, blob func(descriptor) ([]byte, error)) (image, error) {
	var manifest struct {
		Config descriptor   `json:"config"`
		Layers []descriptor `json:"layers"`
	}
	if err := json.Unmarshal(m, &manifest); err != nil {
		return image{}, err
	}
	img := image{files: map[string]fileEntry{}}
	b, err := blob(manifest.Config)
	if err != nil {
		return image{}, err
	}
	if err := json.Unmarshal(b, &img.config); err != nil {
		return image{}, err
	}
	for _, l := range manifest.Layers {
		b, err := blob(l)
		if err != nil {
			return image{}, err
		}
		var r io.Reader = bytes.NewReader(b)
		switch {
		case strings.HasSuffix(l.MediaType, "+gzip"), strings.HasSuffix(l.MediaType, ".tar.gzip"):
			if r, err = gzip.NewReader(r); err != nil {
				return image{}, err
			}
		case strings.HasSuffix(l.MediaType, ".tar"):
		default:
			return image{}, fmt.Errorf("the layer %s has the media type %q, which the test cannot read", l.Digest,
				l.MediaType)
		}
		if err := img.applyLayer(r); err != nil {
			return image{}, fmt.Errorf("the layer %s: %w", l.Digest, err)
		}
	}
	return img, nil
}

// applyLayer applies a layer's tar stream to the image's files: its
// whiteouts remove the files of the layers below, and its files are added.
func (img *image) applyLayer(r io.Reader) error {
	type added struct {
		name  string
		entry fileEntry
	}
	var adds []added
	var links [][2]string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		dir, base := path.Split(name)
		switch {
		case base == ".wh..wh..opq":
			img.remove(strings.TrimSuffix(dir, "/"), false)
			continue
		case strings.HasPrefix(base, ".wh."):
			img.remove(dir+strings.TrimPrefix(base, ".wh."), true)
			continue
		}
		e := fileEntry{typeflag: h.Typeflag, mode: h.Mode, uid: h.Uid, gid: h.Gid}
		if h.Typeflag == tar.TypeReg {
			head := make([]byte, 4)
			n, _ := io.ReadFull(tr, head)
			e.elf = bytes.Equal(head[:n], []byte(elf.ELFMAG))
			if name == binaryPath {
				rest, err := io.ReadAll(tr)
				if err != nil {
					return err
				}
				img.binary = append(head[:n], rest...)
			}
		}
		if h.Typeflag == tar.TypeLink {
			links = append(links, [2]string{name, strings.TrimPrefix(path.Clean("/"+h.Linkname), "/")})
		}
		adds = append(adds, added{name, e})
	}
	for _, a := range adds {
		img.files[a.name] = a.entry
	}
	// A hard link is the file it links to.
	for _, l := range links {
		target, ok := img.files[l[1]]
		if !ok {
			return fmt.Errorf("%s links to %s, which does not exist", l[0], l[1])
		}
		img.files[l[0]] = target
	}
	return nil
}

// remove removes what is under dir, and dir itself if self is set.
func (img *image) remove(dir string, self bool) {
	for name := range img.files {
		if self && name == dir || strings.HasPrefix(name, dir+"/") {
			delete(img.files, name)
		}
	}
}

// platforms returns the platforms of images, sorted.
func platforms(images []image) []string {
	var ps []string
	for _, img := range images {
		ps = append(ps, img.platform)
	}
	slices.Sort(ps)
	return ps
}

// elfMachines are the ELF machines of the published platforms.
var elfMachines = map[string]elf.Machine{"linux/amd64": elf.EM_X86_64, "linux/arm64": elf.EM_AARCH64}

// TestImagePlatforms checks that the image is built for linux/amd64 and
// linux/arm64, the platforms that CI publishes (PRD FR-ADMIN-07 AC2), and
// that each platform's image holds a binary for that platform, linked
// statically, as the distroless base needs.
//
// Verifies: FR-ADMIN-07/AC2
func TestImagePlatforms(t *testing.T) {
	images := archive(t)
	if got := platforms(images); !slices.Equal(got, slices.Sorted(maps.Keys(elfMachines))) {
		t.Fatalf("the image is built for %q, want linux/amd64 and linux/arm64", got)
	}
	for _, img := range images {
		if got := img.config.OS + "/" + img.config.Architecture; got != img.platform {
			t.Errorf("%s: the image's configuration says %s", img.platform, got)
		}
		if img.binary == nil {
			t.Errorf("%s: the image has no /%s", img.platform, binaryPath)
			continue
		}
		f, err := elf.NewFile(bytes.NewReader(img.binary))
		if err != nil {
			t.Errorf("%s: /%s: %v", img.platform, binaryPath, err)
			continue
		}
		if f.Machine != elfMachines[img.platform] {
			t.Errorf("%s: /%s is built for %s", img.platform, binaryPath, f.Machine)
		}
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				t.Errorf("%s: /%s is linked dynamically", img.platform, binaryPath)
			}
		}
	}
}

// toolchain names the programs of a build toolchain, a shell, or a package
// manager, none of which the image may have in a bin or sbin directory.
var toolchain = []string{
	"sh", "ash", "bash", "dash", "zsh", "busybox",
	"go", "gofmt", "gcc", "cc", "c++", "g++", "clang", "ld", "as", "make", "git",
	"apk", "apt", "apt-get", "dpkg", "rpm", "pip", "python3", "perl", "node", "npm", "curl", "wget",
}

// TestImageRunsAsNonRootWithoutToolchain checks that the image runs as a
// user other than root, which owns the asset directory but cannot change
// the binary, and that it contains no build toolchain: no executable but
// the storycreep binary, and no shell or package manager either (PRD
// FR-ADMIN-01 AC4).
//
// Verifies: FR-ADMIN-01/AC4
func TestImageRunsAsNonRootWithoutToolchain(t *testing.T) {
	for _, img := range archive(t) {
		t.Run(img.platform, func(t *testing.T) {
			uid, gid, err := numericUser(img.config.Config.User)
			switch {
			case err != nil:
				t.Error(err)
			case uid == 0 || gid == 0:
				t.Errorf("the image runs as %q, which is root's user or group", img.config.Config.User)
			}

			var elfs []string
			for name, e := range img.files {
				if e.elf {
					elfs = append(elfs, name)
				}
				dir, base := path.Split(name)
				if dir := path.Base(dir); (dir == "bin" || dir == "sbin") && slices.Contains(toolchain, base) {
					t.Errorf("the image has /%s", name)
				}
			}
			slices.Sort(elfs)
			if !slices.Equal(elfs, []string{binaryPath}) {
				t.Errorf("the image's executables are %q, want /%s alone", elfs, binaryPath)
			}

			if e := img.files[binaryPath]; e.uid != 0 || e.mode&0o022 != 0 {
				t.Errorf("/%s is owned by %d, with the mode %o; want root, and writable by root alone",
					binaryPath, e.uid, e.mode)
			}
			assets, ok := img.files["var/lib/storycreep/assets"]
			if err == nil && (!ok || assets.typeflag != tar.TypeDir || assets.uid != uid || assets.gid != gid) {
				t.Errorf("/var/lib/storycreep/assets is %+v, want a directory of the user %d and the group %d, "+
					"so that the volume mounted on it is writable", assets, uid, gid)
			}
		})
	}
}

// numericUser reads the user of an image's configuration, which must be
// written as uid:gid, so that a runtime can tell that it is not root without
// the image's /etc/passwd.
func numericUser(user string) (uid, gid int, err error) {
	u, g, ok := strings.Cut(user, ":")
	uid, errU := strconv.Atoi(u)
	gid, errG := strconv.Atoi(g)
	if !ok || errors.Join(errU, errG) != nil || uid < 0 || gid < 0 {
		return 0, 0, fmt.Errorf("the image runs as %q, want a numeric uid:gid", user)
	}
	return uid, gid, nil
}

// TestImageConfig checks how the image runs: storycreep serve, on port 8080,
// with storycreep healthcheck as its health check, and labelled with its
// source and licence.
func TestImageConfig(t *testing.T) {
	for _, img := range archive(t) {
		c := img.config.Config
		if !slices.Equal(c.Entrypoint, []string{"/" + binaryPath}) || !slices.Equal(c.Cmd, []string{"serve"}) {
			t.Errorf("%s: the image runs %q %q, want /%s serve", img.platform, c.Entrypoint, c.Cmd, binaryPath)
		}
		if _, ok := c.ExposedPorts["8080/tcp"]; !ok || len(c.ExposedPorts) != 1 {
			t.Errorf("%s: the image exposes %v, want 8080/tcp", img.platform, slices.Collect(maps.Keys(c.ExposedPorts)))
		}
		if want := []string{"CMD", "/" + binaryPath, "healthcheck"}; c.Healthcheck == nil ||
			!slices.Equal(c.Healthcheck.Test, want) {
			t.Errorf("%s: the image's health check is %+v, want %q", img.platform, c.Healthcheck, want)
		}
		for label, want := range map[string]string{
			"org.opencontainers.image.source":   "https://github.com/CyberCute-Studio/storycreep",
			"org.opencontainers.image.licenses": "AGPL-3.0-only",
		} {
			if got := c.Labels[label]; got != want {
				t.Errorf("%s: the label %s is %q, want %q", img.platform, label, got, want)
			}
		}
	}
}
