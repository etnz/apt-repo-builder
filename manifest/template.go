package manifest

import (
	"strings"
	"text/template"
)

// templateEngine handles text template rendering with variable substitution.
type templateEngine struct {
	defines map[string]string
	funcs   template.FuncMap
}

// newTemplateEngine creates a new engine with the provided global definitions.
func newTemplateEngine(defines map[string]string) *templateEngine {
	d := make(map[string]string)
	for k, v := range defines {
		d[k] = v
	}
	return &templateEngine{
		defines: d,
		funcs:   template.FuncMap{},
	}
}

// sub creates a new templateEngine that inherits the parent's definitions
// and adds (or overrides) them with the provided local definitions.
func (e *templateEngine) sub(locals map[string]string) *templateEngine {
	newDefines := make(map[string]string)
	for k, v := range e.defines {
		newDefines[k] = v
	}
	for k, v := range locals {
		newDefines[k] = v
	}
	return &templateEngine{
		defines: newDefines,
		funcs:   e.funcs,
	}
}

// render executes the provided text as a template using the engine's definitions.
// If the text does not contain "{{", it is returned as-is.
func (e *templateEngine) render(name, text string) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := template.New(name).Funcs(e.funcs).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := t.Execute(&buf, e.defines); err != nil {
		return "", err
	}
	return buf.String(), nil
}
