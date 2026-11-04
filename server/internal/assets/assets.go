// Package assets — манифест и раздача игровых ресурсов.
//
// Источники: (1) каталог AssetsDir на диске (загруженные владельцем ресурсы, деплой их не трогает);
// (2) временные заглушки (placeholder), генерируемые детерминированно — они нужны только для проверки
// загрузчика клиента и перекрываются реальными файлами с тем же путём.
// Раздаются только пути из индекса: произвольные пути ФС недоступны.
package assets

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type File struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Group       string `json:"group"`
	Placeholder bool   `json:"placeholder,omitempty"`
}

type Manifest struct {
	Version string `json:"version"` // хэш списка файлов: меняется при любом изменении состава/содержимого
	Files   []File `json:"files"`
}

type entry struct {
	File
	disk string // путь на диске, пусто для заглушки
	seed string
}

type Service struct {
	dir       string
	mu        sync.Mutex
	scannedAt time.Time
	index     map[string]*entry
	manifest  Manifest
	hashes    map[string]hashed // кэш хэшей файлов диска по (размер, mtime)
	phHash    map[string]string
}

type hashed struct {
	size  int64
	mtime time.Time
	sum   string
}

// Заглушки: размеры подобраны, чтобы проверить докачку (несколько МБ), но не нагружать сервер.
var placeholders = []struct {
	path string
	size int
}{
	{"start/placeholder-a.bin", 256 << 10},
	{"start/placeholder-b.bin", 512 << 10},
	{"bg/placeholder-big-1.bin", 6 << 20},
	{"bg/placeholder-big-2.bin", 6 << 20},
}

func New(dir string) *Service {
	return &Service{dir: dir, hashes: map[string]hashed{}, phHash: map[string]string{}}
}

// genPlaceholder возвращает детерминированное содержимое заглушки.
func genPlaceholder(name string, size int) []byte {
	out := make([]byte, 0, size+32)
	var ctr [8]byte
	for i := uint64(0); len(out) < size; i++ {
		binary.BigEndian.PutUint64(ctr[:], i)
		h := sha256.Sum256(append([]byte("lunexgame-placeholder:"+name+":"), ctr[:]...))
		out = append(out, h[:]...)
	}
	return out[:size]
}

func (s *Service) rescan() error {
	idx := map[string]*entry{}
	for _, p := range placeholders {
		sum, ok := s.phHash[p.path]
		if !ok {
			h := sha256.Sum256(genPlaceholder(p.path, p.size))
			sum = hex.EncodeToString(h[:])
			s.phHash[p.path] = sum
		}
		idx[p.path] = &entry{File: File{Path: p.path, Size: int64(p.size), SHA256: sum, Group: group(p.path), Placeholder: true}, seed: p.path}
	}
	if s.dir != "" {
		seen := map[string]bool{}
		err := filepath.WalkDir(s.dir, func(full string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // недоступный файл пропускаем, остальное раздаём
			}
			rel, _ := filepath.Rel(s.dir, full)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() || !strings.Contains(rel, "/") {
				return nil // скрытые, не обычные файлы и файлы в корне (вне групп) не публикуем
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			seen[full] = true
			sum := ""
			if c, ok := s.hashes[full]; ok && c.size == info.Size() && c.mtime.Equal(info.ModTime()) {
				sum = c.sum
			} else {
				f, err := os.Open(full)
				if err != nil {
					return nil
				}
				h := sha256.New()
				_, err = io.Copy(h, f)
				f.Close()
				if err != nil {
					return nil
				}
				sum = hex.EncodeToString(h.Sum(nil))
				s.hashes[full] = hashed{info.Size(), info.ModTime(), sum}
			}
			idx[rel] = &entry{File: File{Path: rel, Size: info.Size(), SHA256: sum, Group: group(rel)}, disk: full}
			return nil
		})
		if err != nil {
			return err
		}
		for k := range s.hashes {
			if !seen[k] {
				delete(s.hashes, k)
			}
		}
	}
	paths := make([]string, 0, len(idx))
	for p := range idx {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	m := Manifest{Files: make([]File, 0, len(paths))}
	vh := sha256.New()
	for _, p := range paths {
		e := idx[p]
		m.Files = append(m.Files, e.File)
		vh.Write([]byte(p + "\x00" + e.SHA256 + "\x00"))
	}
	m.Version = hex.EncodeToString(vh.Sum(nil))[:16]
	s.index, s.manifest, s.scannedAt = idx, m, time.Now()
	return nil
}

func group(p string) string { return strings.SplitN(p, "/", 2)[0] }

func (s *Service) current() (Manifest, map[string]*entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index == nil || time.Since(s.scannedAt) > 5*time.Second {
		if err := s.rescan(); err != nil {
			return Manifest{}, nil, err
		}
	}
	return s.manifest, s.index, nil
}

func (s *Service) ManifestHandler(w http.ResponseWriter, r *http.Request) {
	m, _, err := s.current()
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(m)
}

// FileHandler раздаёт файл по пути относительно prefix (Range/If-Range поддерживаются через ServeContent).
func (s *Service) FileHandler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		if rel == "" || path.Clean("/"+rel) != "/"+rel {
			http.NotFound(w, r)
			return
		}
		_, idx, err := s.current()
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		e, ok := idx[rel]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"`+e.SHA256+`"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if e.disk == "" {
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(genPlaceholder(e.seed, int(e.Size))))
			return
		}
		f, err := os.Open(e.disk)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || st.Size() != e.Size {
			// файл изменился после сканирования — не отдаём несогласованные данные
			http.Error(w, "changed", http.StatusConflict)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, f)
	})
}
