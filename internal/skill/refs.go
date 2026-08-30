package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Site is the published documentation. Nothing in the Skill may use a
// repository-relative link out of the Skill: a globally installed copy has no
// `docs/` beside it, so every reference out is an absolute URL.
const Site = "https://filippolmt.github.io/proximo/"

// A generated block is delimited so the surrounding prose stays hand-written:
// the Skill is an order of questions, and only the contracts inside it — the
// label table, the two checklists — have their single source in `docs/`.
var blockPattern = regexp.MustCompile(`(?s)(<!-- generated:start source=(\S+) -->\n).*?(<!-- generated:end -->)`)

var linkPattern = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

var headingPattern = regexp.MustCompile(`^## (.+)$`)

var nonSlug = regexp.MustCompile(`[^a-z0-9 -]`)

// itemStart matches the first line of an ordered list.
var itemStart = regexp.MustCompile(`^\d+\. `)

// Refs regenerates every generated block in the Skill source under root,
// returning the full new content of each file that carries one, by path.
// Nothing is written: the caller decides whether this is a check or an update.
func Refs(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	src := filepath.Join(root, "skills", Name)

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rendered, err := render(root, data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if rendered != nil {
			out[path] = rendered
		}
		return nil
	})
	return out, err
}

// render replaces the body of every generated block in one file, or returns nil
// when the file has none.
func render(root string, data []byte) ([]byte, error) {
	if !blockPattern.Match(data) {
		return nil, nil
	}
	var failure error
	result := blockPattern.ReplaceAllFunc(data, func(block []byte) []byte {
		m := blockPattern.FindSubmatch(block)
		body, err := contract(root, string(m[2]))
		if err != nil {
			failure = err
			return block
		}
		return []byte(string(m[1]) + body + string(m[3]))
	})
	return result, failure
}

// contract extracts the contract a source reference names: the table or the
// ordered list inside the section at `<file>.md#<anchor>`, with every link
// rewritten to the published site.
//
// One block per section, and it runs to the first blank line — the sources are
// a label table and two checklists, each an unbroken run of lines. A blank line
// introduced inside one truncates the block, which shows up as a diff in the
// generated file rather than as silence.
func contract(root, source string) (string, error) {
	file, anchor, ok := strings.Cut(source, "#")
	if !ok {
		return "", fmt.Errorf("source %q names no section anchor", source)
	}
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return "", err
	}
	body := section(string(data), anchor)
	if body == nil {
		return "", fmt.Errorf("%s has no section #%s", file, anchor)
	}
	block := firstBlock(body)
	if block == nil {
		return "", fmt.Errorf("%s#%s holds no table or ordered list to generate from", file, anchor)
	}
	return absolutize(strings.Join(block, "\n")+"\n", file)
}

// section returns the lines under the `##` heading whose GitHub anchor is the
// one asked for, excluding the heading itself.
func section(doc, anchor string) []string {
	var body []string
	inside := false
	for _, line := range strings.Split(doc, "\n") {
		m := headingPattern.FindStringSubmatch(line)
		if m == nil {
			if inside {
				body = append(body, line)
			}
			continue
		}
		if inside {
			return body
		}
		if slug(m[1]) == anchor {
			inside = true
			body = []string{}
		}
	}
	return body
}

// firstBlock returns the first unbroken run of lines starting a table or an
// ordered list.
func firstBlock(body []string) []string {
	for i, line := range body {
		if !strings.HasPrefix(line, "|") && !itemStart.MatchString(line) {
			continue
		}
		end := i
		for end < len(body) && strings.TrimSpace(body[end]) != "" {
			end++
		}
		return body[i:end]
	}
	return nil
}

// absolutize rewrites the repository-relative links of one docs file to the
// published site. A same-file `#anchor` resolves against the file it came from,
// which is why the source file is passed in. A link shape with no published
// counterpart is an error, not a passthrough: it would ship a dead reference
// into every globally installed copy, where no `docs/` sits beside the Skill.
func absolutize(text, source string) (string, error) {
	self, err := resolve(source, filepath.Base(source))
	if err != nil {
		return "", err
	}
	var failed string
	out := linkPattern.ReplaceAllStringFunc(text, func(link string) string {
		m := linkPattern.FindStringSubmatch(link)
		label, target := m[1], m[2]
		switch {
		case strings.Contains(target, "://"):
			return link
		case strings.HasPrefix(target, "#"):
			return fmt.Sprintf("[%s](%s%s%s)", label, Site, self, target)
		case strings.HasSuffix(target, ".md") || strings.Contains(target, ".md#"):
			published, err := resolve(source, target)
			if err != nil {
				failed = target
				return link
			}
			return fmt.Sprintf("[%s](%s%s)", label, Site, published)
		default:
			failed = target
			return link
		}
	})
	if failed != "" {
		return "", fmt.Errorf("link target %q in %s has no published counterpart", failed, source)
	}
	return out, nil
}

// resolve maps a link relative to a docs file onto its page on the published
// site, which serves `docs/` as its root. The whole path is kept, not just the
// file name: `adr/0003-….md` is a real page one directory down, and flattening
// it would ship a 404 into every installed copy.
func resolve(source, target string) (string, error) {
	file, anchor, _ := strings.Cut(target, "#")
	rel := filepath.Clean(filepath.Join(filepath.Dir(source), file))
	rel, err := filepath.Rel("docs", rel)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside docs/, so it has no page on the site", target)
	}
	page := strings.TrimSuffix(filepath.ToSlash(rel), ".md") + ".html"
	if anchor != "" {
		page += "#" + anchor
	}
	return page, nil
}

// slug reproduces GitHub's heading anchors: lowercase, punctuation dropped,
// spaces hyphenated.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = nonSlug.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}
