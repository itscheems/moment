package webassets

import (
	"io/fs"
	"testing"
)

func TestProductionIndexIsEmbedded(t *testing.T) {
	assets, err := Public()
	if err != nil {
		t.Fatal(err)
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(index) == 0 {
		t.Fatal("embedded index.html is empty")
	}
}
