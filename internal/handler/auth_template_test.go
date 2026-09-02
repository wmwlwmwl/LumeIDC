package handler

import (
	"html/template"
	"testing"
)

func TestAuthTemplateParses(t *testing.T) {
	if _, err := template.ParseFS(authFS, "templates/auth.html"); err != nil {
		t.Fatalf("auth template parse failed: %v", err)
	}
}
