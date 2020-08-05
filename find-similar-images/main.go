// Command find-similar-images scans directory for jpeg images and reports any
// similar images (potential duplicates).
package main

import (
	"context"
	"flag"
	"image"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/artyom/phash"
	"github.com/disintegration/imaging"
	"golang.org/x/sync/errgroup"
)

func main() {
	log.SetFlags(0)
	flag.Parse()
	if err := run(flag.Arg(0)); err != nil {
		log.Fatal(err)
	}
}

// minDiff is a phash distance similarity threshold: phash distance above this
// threshold are treated as different images, images with phash distance equal
// or below this threshold are reported as likely duplicates
const minDiff = 5

func run(dir string) error {
	dups := &duptrack{}
	group, ctx := errgroup.WithContext(context.Background())
	ch := make(chan string)
	walkFunc := func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if ext := filepath.Ext(p); !(strings.EqualFold(ext, ".jpg") || strings.EqualFold(ext, ".jpeg")) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- p:
		}
		return nil
	}
	group.Go(func() error {
		defer close(ch)
		return filepath.Walk(dir, walkFunc)
	})
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		group.Go(func() error {
			for p := range ch {
				if err := dups.scan(p); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return group.Wait()
}

type duptrack struct {
	mu sync.Mutex
	ms []meta
}

func (d *duptrack) scan(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	img, err := imaging.Decode(f, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}
	x, err := phash.Get(img, func(img image.Image, w, h int) image.Image {
		return imaging.Resize(img, w, h, imaging.Lanczos)
	})
	if err != nil {
		return err
	}
	info := meta{hash: x, name: p}

	d.mu.Lock()
	defer d.mu.Unlock()
	i := sort.Search(len(d.ms), func(i int) bool { return d.ms[i].hash >= info.hash })
	if i == len(d.ms) {
		if i != 0 {
			info2 := d.ms[i-1]
			if diff := phash.Distance(info.hash, info2.hash); diff <= minDiff {
				log.Printf("close match: %q has phash close (%x, dist=%d) to %q", p, info.hash, diff, info2.name)
			}
		}
		d.ms = append(d.ms, info)
		return nil
	}
	if d.ms[i].hash == info.hash {
		log.Printf("possible duplicate: %q has the same phash (%x) as %q", p, info.hash, d.ms[i].name)
		return nil
	}
	// the index is [i] here, and not [i+1], because this check is *before*
	// info is inserted into slice, so an element that would be to its right is
	// still at position [i]
	info2 := d.ms[i]
	if diff := phash.Distance(info.hash, info2.hash); diff <= minDiff {
		log.Printf("close match: %q has phash close (%x, dist=%d) to %q", p, info.hash, diff, info2.name)
	}
	if i > 0 {
		info2 = d.ms[i-1]
		if diff := phash.Distance(info.hash, info2.hash); diff <= minDiff {
			log.Printf("close match: %q has phash close (%x, dist=%d) to %q", p, info.hash, diff, info2.name)
		}
	}

	ms2 := d.ms[:i+1]
	tail := make([]meta, len(d.ms[i:]))
	copy(tail, d.ms[i:])
	ms2[i] = info
	d.ms = append(ms2, tail...)
	return nil
}

type meta struct {
	hash uint64
	name string
}
