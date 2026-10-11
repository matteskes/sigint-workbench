package settings

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// setupMarker annotates keys the setup screen created from scratch.
const setupMarker = "# Configured via the setup screen (SPEC §20)."

// mapValue returns the value node for key in mapping m, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setValueNode replaces or creates key in mapping m, preserving any
// existing comments on the pair. createComment marks freshly created
// keys with the setup marker.
func setValueNode(m *yaml.Node, key string, value *yaml.Node, createComment bool) {
	if existing := mapValue(m, key); existing != nil {
		head, line := existing.HeadComment, existing.LineComment
		*existing = *value
		existing.HeadComment, existing.LineComment = head, line
		return
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	if createComment {
		keyNode.HeadComment = setupMarker
	}
	m.Content = append(m.Content, keyNode, value)
}

// ensureMapping walks or creates the nested mappings along segs,
// marking created keys with the setup marker.
func ensureMapping(root *yaml.Node, segs []string) (*yaml.Node, error) {
	m := root
	for _, seg := range segs {
		child := mapValue(m, seg)
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg, HeadComment: setupMarker}
			m.Content = append(m.Content, keyNode, child)
			m = child
			continue
		}
		if child.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s is not a mapping", seg)
		}
		m = child
	}
	return m, nil
}

// setScalar sets a dotted path to a scalar value, creating missing
// parent mappings and preserving comments on existing keys.
func setScalar(root *yaml.Node, path string, v any) error {
	segs := strings.Split(path, ".")
	parent, err := ensureMapping(root, segs[:len(segs)-1])
	if err != nil {
		return fmt.Errorf("settings: %s: %w", path, err)
	}
	node, err := scalarNode(v)
	if err != nil {
		return fmt.Errorf("settings: %s: %w", path, err)
	}
	setValueNode(parent, segs[len(segs)-1], node, false)
	return nil
}

// removeKey deletes a dotted path when present; a no-op when absent.
func removeKey(root *yaml.Node, path string) {
	segs := strings.Split(path, ".")
	m := root
	for _, seg := range segs[:len(segs)-1] {
		if m = mapValue(m, seg); m == nil {
			return
		}
	}
	last := segs[len(segs)-1]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == last {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// setList replaces the sequence at top-level key from coerced items,
// emitting fields in fieldOrder (skipping absent optional values).
func setList(root *yaml.Node, key string, items []map[string]any, fieldOrder []string) error {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, item := range items {
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, name := range fieldOrder {
			v, ok := item[name]
			if !ok || v == nil {
				continue
			}
			if s, isStr := v.(string); isStr && s == "" {
				continue
			}
			node, err := scalarNode(v)
			if err != nil {
				return fmt.Errorf("settings: %s.%s: %w", key, name, err)
			}
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, node)
		}
		if len(m.Content) == 0 {
			return fmt.Errorf("settings: %s: device with no fields", key)
		}
		seq.Content = append(seq.Content, m)
	}
	setValueNode(root, key, seq, true)
	return nil
}

// marshalNode re-emits the edited document in the shipped configs'
// house style: 2-space indent and a blank line before each top-level
// section. yaml.Marshal alone re-indents every nested block to its
// 4-space default and silently deletes blank lines (yaml.v3 cannot
// represent them), so a one-knob save rewrote the whole file —
// observed live as a 75-line git diff for a single threshold change.
func marshalNode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return sectionBlanks(buf.Bytes()), nil
}

// sectionBlanks re-inserts one blank line before every top-level key
// (except the first), above the comment run heading it when present,
// matching the shipped configs' layout. Comment runs may contain
// internal blank lines (yaml.v3 embeds them in the HeadComment string
// and re-emits them), so the ascent steps over blanks that have
// another comment above them; blanks that separate comment runs are
// unrecoverable at parse time only when no comment follows, in which
// case a blank already exists and nothing is inserted.
func sectionBlanks(out []byte) []byte {
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	res := make([]string, 0, len(lines)+4)
	firstKey := true
	for _, ln := range lines {
		if isTopKey(ln) && !firstKey {
			j := len(res)
			for j > 0 {
				prev := res[j-1]
				if strings.HasPrefix(prev, "#") {
					j--
					continue
				}
				// Blank inside a comment run: keep ascending when
				// another comment line sits above it.
				if prev == "" && j >= 2 && strings.HasPrefix(res[j-2], "#") {
					j--
					continue
				}
				break
			}
			// Only keys headed by a comment run get the separator
			// blank: compact key blocks under one header comment
			// (recorder.yaml's listen_port) stay compact, matching
			// the shipped layout.
			if j < len(res) && j > 0 && strings.TrimSpace(res[j-1]) != "" {
				res = append(res[:j], append([]string{""}, res[j:]...)...)
			}
		}
		if isTopKey(ln) {
			firstKey = false
		}
		res = append(res, ln)
	}
	return []byte(strings.Join(res, "\n") + "\n")
}

// isTopKey reports whether ln is a `key:` mapping entry at column 0.
func isTopKey(ln string) bool {
	if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, " ") {
		return false
	}
	k, _, ok := strings.Cut(ln, ":")
	return ok && k != "" && !strings.ContainsAny(k, " \t")
}

// scalarNode builds a yaml scalar for v; the encoder styles it.
func scalarNode(v any) (*yaml.Node, error) {
	switch t := v.(type) {
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t)}, nil
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(t, 10)}, nil
	case float64:
		// Tag-free on purpose: an explicit !!float tag is what made
		// integral saves emit `!!float -57` (the plain form re-reads
		// as an int, so the encoder pinned the type). Every consumer
		// loads numerically — the typed configs decode either — and
		// the shipped files carry bare numbers (threshold_db: -60).
		return &yaml.Node{Kind: yaml.ScalarNode,
			Value: strconv.FormatFloat(t, 'g', -1, 64)}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}
