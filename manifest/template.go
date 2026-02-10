package manifest

import (
	"fmt"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// templateEngine handles text template rendering with variable substitution.
type templateEngine struct {
	defines map[string]string
	funcs   template.FuncMap
}

// newTemplateEngine creates a new engine with the provided global definitions.
func newTemplateEngine(defines map[string]string) (*templateEngine, error) {
	finalDefines := make(map[string]string)
	e := &templateEngine{
		defines: finalDefines,
		funcs:   template.FuncMap{},
	}

	sorted, err := sortLocals(defines)
	if err != nil {
		return nil, err
	}

	for _, kv := range sorted {
		val, err := e.renderWith(fmt.Sprintf("define.%s", kv.key), kv.value, finalDefines)
		if err != nil {
			return nil, err
		}
		finalDefines[kv.key] = val
	}
	return e, nil
}

// sub creates a new templateEngine that inherits the parent's definitions
// and adds (or overrides) them with the provided local definitions.
func (e *templateEngine) sub(locals map[string]string) (*templateEngine, error) {
	newDefines := make(map[string]string)
	for k, v := range e.defines {
		newDefines[k] = v
	}

	sorted, err := sortLocals(locals)
	if err != nil {
		return nil, err
	}

	for _, kv := range sorted {
		val, err := e.renderWith(fmt.Sprintf("define.%s", kv.key), kv.value, newDefines)
		if err != nil {
			return nil, err
		}
		newDefines[kv.key] = val
	}
	return &templateEngine{
		defines: newDefines,
		funcs:   e.funcs,
	}, nil
}

// render executes the provided text as a template using the engine's definitions.
// If the text does not contain "{{", it is returned as-is.
func (e *templateEngine) render(name, text string) (string, error) {
	return e.renderWith(name, text, e.defines)
}

func (e *templateEngine) renderWith(name, text string, defines map[string]string) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := template.New(name).Funcs(e.funcs).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", name, err)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, defines); err != nil {
		return "", fmt.Errorf("executing template %s: %w", name, err)
	}
	return buf.String(), nil
}

type kvPair struct {
	key, value string
}

func sortLocals(locals map[string]string) ([]kvPair, error) {
	keys := make([]string, 0, len(locals))
	for k := range locals {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	deps := make(map[string][]string)
	for _, k := range keys {
		v := locals[k]
		if !strings.Contains(v, "{{") {
			continue
		}

		trees, err := parse.Parse(k, v, "{{", "}}")
		if err != nil {
			return nil, fmt.Errorf("parsing template for define.%s: %w", k, err)
		}

		var vars []string
		var walk func(parse.Node)
		walk = func(n parse.Node) {
			switch node := n.(type) {
			case *parse.ListNode:
				for _, child := range node.Nodes {
					walk(child)
				}
			case *parse.ActionNode:
				walk(node.Pipe)
			case *parse.PipeNode:
				for _, cmd := range node.Cmds {
					walk(cmd)
				}
			case *parse.CommandNode:
				for _, arg := range node.Args {
					walk(arg)
				}
			case *parse.FieldNode:
				if len(node.Ident) > 0 {
					vars = append(vars, node.Ident[0])
				}
			}
		}

		for _, t := range trees {
			if t.Root != nil {
				walk(t.Root)
			}
		}

		seen := make(map[string]bool)
		for _, d := range vars {
			if _, exists := locals[d]; exists && d != k && !seen[d] {
				deps[k] = append(deps[k], d)
				seen[d] = true
			}
		}
		sort.Strings(deps[k])
	}

	var result []kvPair
	visited := make(map[string]bool)
	visiting := make(map[string]bool)

	var visit func(string) error
	visit = func(n string) error {
		if visiting[n] {
			return fmt.Errorf("cycle detected in defines: %s", n)
		}
		if visited[n] {
			return nil
		}
		visiting[n] = true
		for _, d := range deps[n] {
			if err := visit(d); err != nil {
				return err
			}
		}
		visiting[n] = false
		visited[n] = true
		result = append(result, kvPair{key: n, value: locals[n]})
		return nil
	}

	for _, k := range keys {
		if err := visit(k); err != nil {
			return nil, err
		}
	}

	return result, nil
}
