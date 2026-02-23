package specialmenuimage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	MaxInputBytes  = 10 << 20
	MaxOutputBytes = 150 * 1024
)

type sourceKind string

const (
	sourceImage sourceKind = "image"
	sourcePDF   sourceKind = "pdf"
	sourceDoc   sourceKind = "doc"
	sourceText  sourceKind = "text"
)

func NormalizeToWebP(ctx context.Context, input []byte, fileName string, declaredContentType string) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("empty file")
	}
	if len(input) > MaxInputBytes {
		return nil, fmt.Errorf("file too large (max %dMB)", MaxInputBytes/(1024*1024))
	}

	kind, ext, err := detectSourceKind(input, fileName, declaredContentType)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "bo-special-menu-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "input"+ext)
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		return nil, err
	}

	sourcePath := inputPath
	switch kind {
	case sourceImage:
		// Use input directly.
	case sourcePDF:
		sourcePath, err = renderPDFFirstPageToPNG(ctx, inputPath, tmpDir)
		if err != nil {
			return nil, err
		}
	case sourceDoc, sourceText:
		pdfPath, err := convertDocumentToPDF(ctx, inputPath, tmpDir)
		if err != nil {
			return nil, err
		}
		sourcePath, err = renderPDFFirstPageToPNG(ctx, pdfPath, tmpDir)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported source file")
	}

	output, err := encodeWebPWithLimit(ctx, sourcePath, tmpDir)
	if err != nil {
		return nil, err
	}

	return output, nil
}

func detectSourceKind(input []byte, fileName string, declaredContentType string) (sourceKind, string, error) {
	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(input)))
	declared := strings.ToLower(strings.TrimSpace(declaredContentType))
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))

	if isImageType(detected) || isImageType(declared) || isImageExt(ext) {
		if ext == "" || !isImageExt(ext) {
			switch {
			case strings.Contains(detected, "png"):
				ext = ".png"
			case strings.Contains(detected, "gif"):
				ext = ".gif"
			case strings.Contains(detected, "webp"):
				ext = ".webp"
			default:
				ext = ".jpg"
			}
		}
		return sourceImage, ext, nil
	}

	if strings.Contains(detected, "pdf") || strings.Contains(declared, "pdf") || ext == ".pdf" {
		return sourcePDF, ".pdf", nil
	}

	if isOfficeType(detected) || isOfficeType(declared) || ext == ".doc" || ext == ".docx" || (ext == ".docx" && strings.Contains(detected, "zip")) {
		if ext != ".doc" && ext != ".docx" {
			ext = ".docx"
		}
		return sourceDoc, ext, nil
	}

	if strings.HasPrefix(detected, "text/plain") || strings.HasPrefix(declared, "text/plain") || ext == ".txt" {
		return sourceText, ".txt", nil
	}

	return "", "", errors.New("file type not allowed")
}

func isImageType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(ct, "image/jpeg") || strings.HasPrefix(ct, "image/png") || strings.HasPrefix(ct, "image/webp") || strings.HasPrefix(ct, "image/gif")
}

func isImageExt(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	default:
		return false
	}
}

func isOfficeType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(ct, "application/msword") || strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
}

func renderPDFFirstPageToPNG(ctx context.Context, pdfPath string, tmpDir string) (string, error) {
	pdftoppmPath, err := findCommandPath("pdftoppm")
	if err != nil {
		return "", errors.New("pdftoppm not available in server runtime")
	}

	outputBase := filepath.Join(tmpDir, "first-page")
	if err := runCommand(ctx, pdftoppmPath, "-f", "1", "-singlefile", "-png", pdfPath, outputBase); err != nil {
		return "", err
	}
	pngPath := outputBase + ".png"
	if _, err := os.Stat(pngPath); err != nil {
		return "", errors.New("could not render first page to image")
	}
	return pngPath, nil
}

func convertDocumentToPDF(ctx context.Context, inputPath string, tmpDir string) (string, error) {
	libreofficePath, err := findCommandPath("libreoffice", "soffice")
	if err != nil {
		return "", errors.New("libreoffice not available in server runtime")
	}

	profileDir := filepath.Join(tmpDir, "libreoffice-profile")
	_ = os.MkdirAll(profileDir, 0o700)

	cmd := exec.CommandContext(
		ctx,
		libreofficePath,
		"--headless",
		"--nologo",
		"--nolockcheck",
		"--nodefault",
		"--nofirststartwizard",
		"--convert-to",
		"pdf:writer_pdf_Export",
		"--outdir",
		tmpDir,
		inputPath,
	)
	cmd.Env = append(os.Environ(),
		"HOME="+tmpDir,
		"UserInstallation=file://"+profileDir,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("libreoffice conversion failed: %w (%s)", err, compactCommandOutput(out))
	}

	expected := filepath.Join(tmpDir, strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))+".pdf")
	if _, err := os.Stat(expected); err == nil {
		return expected, nil
	}

	matches, globErr := filepath.Glob(filepath.Join(tmpDir, "*.pdf"))
	if globErr != nil || len(matches) == 0 {
		return "", errors.New("could not locate converted pdf")
	}
	return matches[0], nil
}

func encodeWebPWithLimit(ctx context.Context, sourcePath string, tmpDir string) ([]byte, error) {
	magickPath, magickArgs, err := pickImageMagickCommand(sourcePath)
	if err != nil {
		return nil, err
	}

	dimensions := []int{1700, 1500, 1300, 1150, 1000, 900, 820, 740, 660, 580, 520, 460}
	qualities := []int{88, 82, 76, 70, 64, 58, 52, 46, 40, 34, 28}
	outputPath := filepath.Join(tmpDir, "normalized.webp")

	best := []byte(nil)
	for _, dim := range dimensions {
		for _, quality := range qualities {
			args := append([]string{}, magickArgs...)
			args = append(
				args,
				"-auto-orient",
				"-strip",
				"-thumbnail", fmt.Sprintf("%dx%d>", dim, dim),
				"-define", "webp:method=6",
				"-quality", strconv.Itoa(quality),
				outputPath,
			)

			if err := runCommand(ctx, magickPath, args...); err != nil {
				return nil, err
			}

			raw, readErr := os.ReadFile(outputPath)
			if readErr != nil {
				return nil, readErr
			}
			if len(raw) == 0 {
				continue
			}

			if len(best) == 0 || len(raw) < len(best) {
				best = raw
			}
			if len(raw) <= MaxOutputBytes {
				return raw, nil
			}
		}
	}

	if len(best) == 0 {
		return nil, errors.New("failed to produce webp image")
	}
	return nil, fmt.Errorf("no se pudo reducir la imagen por debajo de 150KB (resultado minimo: %dKB)", (len(best)+1023)/1024)
}

func pickImageMagickCommand(sourcePath string) (string, []string, error) {
	if magickPath, err := findCommandPath("magick"); err == nil {
		return magickPath, []string{sourcePath + "[0]"}, nil
	}
	if convertPath, err := findCommandPath("convert"); err == nil {
		return convertPath, []string{sourcePath + "[0]"}, nil
	}
	return "", nil, errors.New("imagemagick (magick/convert) not available in server runtime")
}

func findCommandPath(candidates ...string) (string, error) {
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil && strings.TrimSpace(path) != "" {
			return path, nil
		}
	}
	return "", errors.New("command not found")
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed (%s): %w (%s)", name, err, compactCommandOutput(out))
	}
	return nil
}

func compactCommandOutput(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	s = strings.ReplaceAll(s, "\n", " | ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > 280 {
		s = s[:280]
	}
	if s == "" {
		return "no output"
	}
	return s
}
