package serverless

import (
	"fmt"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/mx-space/core/internal/models"
)

func (h *Handler) compileSnippet(snippet *models.SnippetModel) (string, error) {
	h.compiledMu.RLock()
	if cached, ok := h.compiled[snippet.ID]; ok && cached.UpdatedAt.Equal(snippet.UpdatedAt) {
		h.compiledMu.RUnlock()
		return cached.Code, nil
	}
	h.compiledMu.RUnlock()

	result := api.Transform(snippet.Raw, api.TransformOptions{
		Loader:     api.LoaderTS,
		Format:     api.FormatCommonJS,
		Target:     api.ES2020,
		Sourcefile: fmt.Sprintf("%s/%s.ts", snippet.Reference, snippet.Name),
		Charset:    api.CharsetUTF8,
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("transform failed: %s", result.Errors[0].Text)
	}

	code := string(result.Code)
	h.compiledMu.Lock()
	h.compiled[snippet.ID] = compiledSnippet{
		UpdatedAt: snippet.UpdatedAt,
		Code:      code,
	}
	h.compiledMu.Unlock()

	return code, nil
}
