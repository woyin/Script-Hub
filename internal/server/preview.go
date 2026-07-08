package server

import "github.com/script-hub-org/script-hub/internal/frontend"

func generatePreviewHTML(baseURL string) string {
	return frontend.GenerateHTML(baseURL)
}
