package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverAndLoadSelectedAgentSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "summarize", "Summarize source material.", "Read the source carefully.")
	catalog, err := Discover([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := catalog.LoadSelected([]string{"summarize"})
	if err != nil || len(selected) != 1 {
		t.Fatalf("selected=%#v err=%v", selected, err)
	}
	rendered := Render(selected)
	if !strings.Contains(rendered, "Read the source carefully") || !strings.Contains(rendered, "do not grant tools") {
		t.Fatalf("rendered skills = %q", rendered)
	}
	if _, err := catalog.LoadSelected([]string{"missing"}); err == nil {
		t.Fatal("missing selected skill was accepted")
	}
}

func TestDiscoverRejectsInvalidAndDuplicateSkills(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bad"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad", "SKILL.md"), []byte("no frontmatter"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover([]string{root}); err == nil {
		t.Fatal("invalid skill was accepted")
	}
	rootA, rootB := t.TempDir(), t.TempDir()
	writeSkill(t, rootA, "shared", "One.", "Body one.")
	writeSkill(t, rootB, "shared", "Two.", "Body two.")
	if _, err := Discover([]string{rootA, rootB}); err == nil || !strings.Contains(err.Error(), "duplicate skill") {
		t.Fatalf("duplicate skill error = %v", err)
	}
}

func writeSkill(t *testing.T, root, name, description, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
