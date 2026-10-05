package settings

import (
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

// scalarNode builds a yaml scalar for v; the encoder styles it.
func scalarNode(v any) (*yaml.Node, error) {
	switch t := v.(type) {
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t)}, nil
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(t, 10)}, nil
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: fmt.Sprintf("%g", t)}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}
