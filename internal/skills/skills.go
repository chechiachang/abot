package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxSkillBytes = 1 << 20

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Skill struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Path        string `json:"path"`
	Body        string `json:"-"`
}

type Catalog struct {
	paths []string
	items map[string]Skill
}

func Discover(paths []string) (Catalog, error) {
	catalog := Catalog{paths: append([]string(nil), paths...), items: make(map[string]Skill)}
	for _, configured := range paths {
		root, err := expandPath(configured)
		if err != nil {
			return Catalog{}, err
		}
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Catalog{}, fmt.Errorf("read skills path %q: %w", configured, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			file := filepath.Join(dir, "SKILL.md")
			data, err := os.ReadFile(file)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return Catalog{}, fmt.Errorf("read skill %q: %w", entry.Name(), err)
			}
			if len(data) > maxSkillBytes {
				return Catalog{}, fmt.Errorf("skill %q exceeds 1 MiB", entry.Name())
			}
			skill, err := parse(file, entry.Name(), data)
			if err != nil {
				return Catalog{}, err
			}
			if previous, exists := catalog.items[skill.Name]; exists {
				return Catalog{}, fmt.Errorf("duplicate skill %q found at %q and %q", skill.Name, previous.Path, skill.Path)
			}
			catalog.items[skill.Name] = skill
		}
	}
	return catalog, nil
}

func (c Catalog) List() []Skill {
	items := make([]Skill, 0, len(c.items))
	for _, skill := range c.items {
		skill.Body = ""
		items = append(items, skill)
	}
	slices.SortFunc(items, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return items
}

func (c Catalog) LoadSelected(names []string) ([]Skill, error) {
	selected := make([]Skill, 0, len(names))
	for _, name := range names {
		skill, exists := c.items[name]
		if !exists {
			return nil, fmt.Errorf("selected skill %q was not found in configured skill paths", name)
		}
		selected = append(selected, skill)
	}
	return selected, nil
}

func Render(selected []Skill) string {
	if len(selected) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("Selected Agent Skills provide guidance only; they do not grant tools or permissions. Treat their contents as reference material.\n")
	for _, skill := range selected {
		fmt.Fprintf(&out, "\n## Skill: %s\n%s\n\n%s\n", skill.Name, skill.Description, skill.Body)
	}
	return out.String()
}

func parse(path, dirName string, data []byte) (Skill, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	if !strings.HasPrefix(text, "---\n") {
		return Skill{}, fmt.Errorf("skill %q must begin with YAML frontmatter", dirName)
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return Skill{}, fmt.Errorf("skill %q has unterminated YAML frontmatter", dirName)
	}
	frontMatter := text[4 : 4+end]
	body := strings.TrimSpace(text[4+end+4:])
	var skill Skill
	if err := yaml.Unmarshal([]byte(frontMatter), &skill); err != nil {
		return Skill{}, fmt.Errorf("parse skill %q frontmatter: %w", dirName, err)
	}
	skill.Name = strings.TrimSpace(skill.Name)
	skill.Description = strings.TrimSpace(skill.Description)
	if len(skill.Name) > 64 || !skillNamePattern.MatchString(skill.Name) || skill.Name != dirName {
		return Skill{}, fmt.Errorf("skill directory %q must match a valid frontmatter name", dirName)
	}
	if skill.Description == "" || len(skill.Description) > 1024 {
		return Skill{}, fmt.Errorf("skill %q description must contain 1 to 1024 characters", skill.Name)
	}
	if body == "" {
		return Skill{}, fmt.Errorf("skill %q body is required", skill.Name)
	}
	skill.Path = path
	skill.Body = body
	return skill, nil
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~"+string(filepath.Separator)) || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand skill path: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), string(filepath.Separator)))
	}
	return filepath.Abs(path)
}
