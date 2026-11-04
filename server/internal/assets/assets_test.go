package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fetch(h http.Handler, p string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", p, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func manifest(t *testing.T, s *Service) Manifest {
	w := httptest.NewRecorder()
	s.ManifestHandler(w, httptest.NewRequest("GET", "/m", nil))
	var m Manifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPlaceholdersAndHashes(t *testing.T) {
	s := New("")
	m := manifest(t, s)
	if len(m.Files) != len(placeholders) {
		t.Fatalf("файлов %d", len(m.Files))
	}
	h := s.FileHandler("/a/")
	for _, f := range m.Files {
		if !f.Placeholder {
			t.Fatal("заглушка должна быть помечена")
		}
		w := fetch(h, "/a/"+f.Path, nil)
		sum := sha256.Sum256(w.Body.Bytes())
		if w.Code != 200 || hex.EncodeToString(sum[:]) != f.SHA256 || int64(w.Body.Len()) != f.Size {
			t.Fatalf("%s: код %d, хэш/размер не совпали", f.Path, w.Code)
		}
	}
}

func TestRange(t *testing.T) {
	s := New("")
	h := s.FileHandler("/a/")
	full := fetch(h, "/a/start/placeholder-a.bin", nil).Body.Bytes()
	w := fetch(h, "/a/start/placeholder-a.bin", map[string]string{"Range": "bytes=1000-1999"})
	if w.Code != 206 || string(w.Body.Bytes()) != string(full[1000:2000]) {
		t.Fatalf("Range: %d", w.Code)
	}
}

func TestOverrideAndSafety(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "start"), 0o755)
	os.WriteFile(filepath.Join(d, "start", "placeholder-a.bin"), []byte("real"), 0o644) // перекрывает заглушку
	os.WriteFile(filepath.Join(d, "start", "new.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(d, "start", ".secret"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(d, "rootfile.txt"), []byte("x"), 0o644)
	s := New(d)
	m := manifest(t, s)
	var gotReal, gotNew bool
	for _, f := range m.Files {
		switch f.Path {
		case "start/placeholder-a.bin":
			gotReal = !f.Placeholder && f.Size == 4
		case "start/new.txt":
			gotNew = f.Size == 5
		case "start/.secret", "rootfile.txt":
			t.Fatalf("%s не должен публиковаться", f.Path)
		}
	}
	if !gotReal || !gotNew {
		t.Fatal("переопределение/новый файл не найдены")
	}
	h := s.FileHandler("/a/")
	for _, bad := range []string{"/a/../x", "/a/start/../../etc/passwd", "/a/start/.secret", "/a/rootfile.txt", "/a/", "/a/nope"} {
		if c := fetch(h, bad, nil).Code; c != 404 {
			t.Errorf("%s -> %d, ожидалось 404", bad, c)
		}
	}
	if !strings.Contains(fetch(h, "/a/start/new.txt", nil).Body.String(), "hello") {
		t.Fatal("содержимое")
	}
}

func TestVersionChanges(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "bg"), 0o755)
	s := New(d)
	v1 := manifest(t, s).Version
	os.WriteFile(filepath.Join(d, "bg", "x.bin"), []byte("1"), 0o644)
	s.mu.Lock()
	s.index = nil // принудительный пересбор (в бою — раз в 5 с)
	s.mu.Unlock()
	if v2 := manifest(t, s).Version; v1 == v2 {
		t.Fatal("версия не изменилась")
	}
}
