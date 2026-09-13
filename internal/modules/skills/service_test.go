package skills

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestUnpackRejectsUnsafeArchives(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		mode       os.FileMode
	}{{"traversal", "../outside", 0644}, {"symlink", "link", os.ModeSymlink | 0777}, {"absolute", "/outside", 0644}, {"duplicate", "SKILL.md", 0644}} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			f, _ := z.Create("SKILL.md")
			f.Write([]byte("instructions"))
			h := &zip.FileHeader{Name: tc.path}
			h.SetMode(tc.mode)
			f, _ = z.CreateHeader(h)
			f.Write([]byte("data"))
			z.Close()
			if _, e := Unpack(b.Bytes()); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.Create("SKILL.md")
	f.Write([]byte("instructions"))
	z.Close()
	if files, e := Unpack(b.Bytes()); e != nil || string(files["SKILL.md"]) != "instructions" {
		t.Fatal(files, e)
	}
}

func TestSkillMetadata(t *testing.T) {
	for _, raw := range []string{"---\nname: search\ndescription: Search project documentation\n---\n# Skill", "---\r\nname: search\r\ndescription: >\r\n  Search project\r\n  documentation\r\n---\r\n# Skill"} {
		meta, e := ParseMetadata([]byte(raw))
		if e != nil || meta.Name != "search" || !strings.Contains(meta.Description, "documentation") {
			t.Fatal(meta, e)
		}
	}
	for _, raw := range []string{"# no metadata", "---\nname: test\n---\nbody", "---\nname: [bad]\ndescription: test\n---", "---\nname: test\ndescription: " + strings.Repeat("x", 2049) + "\n---"} {
		if _, e := ParseMetadata([]byte(raw)); e == nil {
			t.Fatal("accepted invalid metadata")
		}
	}
}
