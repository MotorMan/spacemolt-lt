package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	s := New(p)
	type data struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	d := data{Name: "foo", N: 42}
	if err := s.Write(d); err != nil {
		t.Fatal(err)
	}
	var out data
	if err := s.Read(&out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "foo" || out.N != 42 {
		t.Fatalf("got %+v", out)
	}
	// corrupt recovery
	_ = os.WriteFile(p, []byte("{bad"), 0644)
	var out2 data
	err := s.Read(&out2)
	if err == nil {
		t.Fatal("expected corrupt error")
	}
}
