package tools

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
	"net/url" // Added for URL parsing/joining
	"strconv" // Added for string conversion

	"github.com/Masterminds/sprig/v3"
	"github.com/minio/minio-go/v7" // Added for Minio client
	"github.com/minio/minio-go/v7/pkg/credentials" // Added for Minio credentials
	// "github.com/pkg/browser" // No longer needed

	"github.com/humanitec/canyon-cli/internal/mcp"
)

// Simple word list for random filenames
var randomWords = []string{
	"apple", "banana", "cherry", "date", "elderberry", "fig", "grape", "honeydew",
	"kiwi", "lemon", "mango", "nectarine", "orange", "papaya", "quince", "raspberry",
	"strawberry", "tangerine", "ugli", "vanilla", "watermelon", "xigua", "yam", "zucchini",
	"red", "green", "blue", "yellow", "purple", "orange", "pink", "brown", "black", "white",
	"happy", "sad", "big", "small", "fast", "slow", "bright", "dark", "shiny", "dull",
}

//go:embed render_csv.html.tmpl
var renderCsvTemplate string

//go:embed render_tree.html.tmpl
var renderTreeTemplate string

//go:embed render_graph.html.tmpl
var renderGraphTemplate string

var funcMap template.FuncMap

func init() {
	// Seed random number generator
	rand.New(rand.NewSource(time.Now().UnixNano()))

	f := func(path string, defaultContent string) string {
		raw, err := os.ReadFile(path)
		if err == nil {
			if len(raw) == 0 {
				_ = os.WriteFile(path, []byte(defaultContent), 0644)
			} else {
				return string(raw)
			}
		}
		return defaultContent
	}

	h, err := os.UserHomeDir()
	if err == nil {
		renderCsvTemplate = f(filepath.Join(h, "canyon-render-csv-template.html.tmpl"), renderCsvTemplate)
		renderTreeTemplate = f(filepath.Join(h, "canyon-render-tree-template.html.tmpl"), renderTreeTemplate)
		renderGraphTemplate = f(filepath.Join(h, "canyon-render-graph-template.html.tmpl"), renderGraphTemplate)
	}

	funcMap = sprig.HtmlFuncMap()
	funcMap["toRawJsonJs"] = func(content interface{}) template.JS {
		raw, _ := json.Marshal(content)
		return template.JS(raw)
	}
}

// generateRandomFilename creates a filename like "word1-word2-word3-12345.html"
func generateRandomFilename() string {
	n := len(randomWords)
	wordsPart := fmt.Sprintf("%d", time.Now().UnixNano()) // Fallback if word list is empty
	if n > 0 {
		word1 := randomWords[rand.Intn(n)]
		word2 := randomWords[rand.Intn(n)]
		word3 := randomWords[rand.Intn(n)]
		wordsPart = fmt.Sprintf("%s-%s-%s", word1, word2, word3)
	}

	// Generate 5 random digits
	digits := fmt.Sprintf("%05d", rand.Intn(100000)) // Generates a number between 0 and 99999, pads with leading zeros if needed

	return fmt.Sprintf("%s-%s.html", wordsPart, digits)
}

// renderAndUploadToMinio takes the rendered HTML buffer, uploads it to Minio,
// and returns a public URL. Configuration is read from environment variables.
func renderAndUploadToMinio(ctx context.Context, buffer *bytes.Buffer) (string, error) {
	// 1. Read configuration from environment variables
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKeyID := os.Getenv("MINIO_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("MINIO_SECRET_ACCESS_KEY")
	bucketName := os.Getenv("MINIO_BUCKET")
	useSSLStr := os.Getenv("MINIO_USE_SSL") // Expect "true" or "false"

	if endpoint == "" || accessKeyID == "" || secretAccessKey == "" || bucketName == "" {
		return "", fmt.Errorf("missing required Minio environment variables (MINIO_ENDPOINT, MINIO_ACCESS_KEY_ID, MINIO_SECRET_ACCESS_KEY, MINIO_BUCKET)")
	}

	useSSL := true // Default to true if not specified or invalid
	if useSSLStr != "" {
		parsedSSL, err := strconv.ParseBool(useSSLStr)
		if err == nil {
			useSSL = parsedSSL
		} else {
			slog.Warn("Invalid MINIO_USE_SSL value, defaulting to true", slog.String("value", useSSLStr), slog.Any("error", err))
		}
	}

	// Remove potential scheme (like https://) from endpoint for Minio client
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid MINIO_ENDPOINT format: %w", err)
	}
	minioEndpoint := endpointURL.Host // Use host:port

	// 2. Initialize Minio client
	minioClient, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create Minio client: %w", err)
	}

	// 3. Generate filename (object name)
	filename := generateRandomFilename()
	objectName := filename // Use filename directly as object name in Minio

	// 4. Upload the content
	// Use PutObject with buffer.Bytes() and buffer.Len()
	uploadInfo, err := minioClient.PutObject(ctx, bucketName, objectName, buffer, int64(buffer.Len()), minio.PutObjectOptions{
		ContentType: "text/html",
		// Consider adding Cache-Control: CacheControl: "public, max-age=...",
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload object to Minio: %w", err)
	}
	slog.Info("Successfully uploaded to Minio", slog.String("bucket", bucketName), slog.String("object", objectName), slog.Int64("size", uploadInfo.Size))

	// 5. Construct the public URL
	// Ensure endpoint has scheme for proper URL construction
	publicURL := fmt.Sprintf("%s/%s/%s", endpoint, bucketName, objectName)

	// Validate and potentially clean up the URL (e.g., remove double slashes if endpoint already has trailing slash)
	parsedPublicURL, err := url.Parse(publicURL)
	if err != nil {
		slog.Error("Failed to parse constructed public URL, returning raw string", slog.String("url", publicURL), slog.Any("error", err))
		return publicURL, nil // Return best effort URL even if parsing fails
	}
	// Basic path cleaning
	parsedPublicURL.Path = filepath.Join(parsedPublicURL.Path) // Should handle extra slashes

	return parsedPublicURL.String(), nil
}

// NewRenderCSVAsTable renders csv as a table and uploads to Minio.
func NewRenderCSVAsTable() mcp.Tool {
	tmpl, err := template.New("").Funcs(funcMap).Parse(renderCsvTemplate)
	if err != nil {
		panic(err)
	}
	return mcp.Tool{
		Name:        "render_csv_as_table_to_minio",
		Description: `This tool renders CSV data as an HTML table and uploads it to Minio, returning a public link. Requires MINIO_* env vars to be set.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"raw":                 map[string]interface{}{"type": "string", "description": "The raw multiline csv content"},
				"first_row_is_header": map[string]interface{}{"type": "boolean", "description": "Whether the first row of csv is the header"},
			},
			"required": []interface{}{"raw"},
		},
		Callable: func(ctx context.Context, arguments map[string]interface{}) ([]mcp.CallToolResponseContent, error) {
			// Validate CSV input
			r := csv.NewReader(strings.NewReader(arguments["raw"].(string)))
			if _, err := r.ReadAll(); err != nil {
				return nil, fmt.Errorf("invalid csv content: %w", err)
			}

			// Render template to buffer
			buffer := new(bytes.Buffer)
			if err := tmpl.Execute(buffer, arguments); err != nil {
				slog.Error("failed to execute csv template", slog.Any("err", err))
				return nil, fmt.Errorf("could not render csv html content: %w", err)
			}

			// Upload and get URL
			publicURL, err := renderAndUploadToMinio(ctx, buffer)
			if err != nil {
				return nil, err // Error already contains details
			}

			return []mcp.CallToolResponseContent{mcp.NewTextToolResponseContent("CSV rendered and uploaded: " + publicURL)}, nil
		},
	}
}

// NewRenderTreeAsTree renders a hierarchy and uploads to Minio.
func NewRenderTreeAsTree() mcp.Tool {
	tmpl, err := template.New("").Funcs(funcMap).Parse(renderTreeTemplate)
	if err != nil {
		panic(err)
	}
	return mcp.Tool{
		Name:        "render_data_as_tree_to_minio",
		Description: `This tool renders hierarchical data (like a tree structure) as HTML and uploads it to Minio, returning a public link. Requires MINIO_* env vars to be set.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"root": map[string]interface{}{"$ref": "#/$defs/node", "description": "The root of the tree structure"},
			},
			"required": []interface{}{"root"},
			"$defs": map[string]interface{}{
				"node": map[string]interface{}{
					"type": "object",
					"description": "A node in the tree structure",
					"properties": map[string]interface{}{
						"name":     map[string]interface{}{"type": "string", "description": "The name of the node"},
						"class":    map[string]interface{}{"type": "string", "description": "The class of the node. Well known classes are: 'org', 'app', 'env_type', 'env', 'workload', 'resource', and 'other' but arbitrary strings can be used too"},
						"data":     map[string]interface{}{"type": "object", "description": "Arbitrary additional metadata to include on the node visualisation"},
						"children": map[string]interface{}{"type": "array", "items": map[string]interface{}{"$ref": "#/$defs/node"}},
					},
					"required": []interface{}{"name", "class"},
				},
			},
		},
		Callable: func(ctx context.Context, arguments map[string]interface{}) ([]mcp.CallToolResponseContent, error) {
			// Render template to buffer
			buffer := new(bytes.Buffer)
			if err := tmpl.Execute(buffer, arguments); err != nil { // Pass arguments directly
				slog.Error("failed to execute tree template", slog.Any("err", err))
				return nil, fmt.Errorf("could not render tree html content: %w", err)
			}

			// Upload and get URL
			publicURL, err := renderAndUploadToMinio(ctx, buffer)
			if err != nil {
				return nil, err // Error already contains details
			}

			return []mcp.CallToolResponseContent{mcp.NewTextToolResponseContent("Tree rendered and uploaded: " + publicURL)}, nil
		},
	}
}

// NewRenderNetworkAsGraph renders a network graph and uploads to Minio.
func NewRenderNetworkAsGraph() mcp.Tool {
	tmpl, err := template.New("").Funcs(funcMap).Parse(renderGraphTemplate)
	if err != nil {
		panic(err)
	}
	return mcp.Tool{
		Name:        "render_network_as_graph_to_minio",
		Description: `This tool renders an interconnected network as a force-directed graph in HTML and uploads it to Minio, returning a public link. Requires MINIO_* env vars to be set.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodes": map[string]interface{}{"type": "array", "description": "The list of nodes in the network", "items": map[string]interface{}{
					"type":        "object",
					"description": "A node in the network graph",
					"properties": map[string]interface{}{
						"id":    map[string]interface{}{"type": "string"},
						"class": map[string]interface{}{"type": "string", "description": "The class of the node. Well known classes are: 'org', 'app', 'env_type', 'env', 'workload', 'resource', and 'other' but arbitrary strings can be used too"},
						"data":  map[string]interface{}{"type": "object", "description": "Arbitrary additional metadata to include on the node visualisation"},
					},
					"required": []interface{}{"id", "class"},
				}},
				"links": map[string]interface{}{"type": "array", "description": "The list of links between nodes in the network", "items": map[string]interface{}{
					"type":        "object",
					"description": "A link in the network graph",
					"properties": map[string]interface{}{
						"source":      map[string]interface{}{"type": "string", "description": "The source node id of the link"},
						"target":      map[string]interface{}{"type": "string", "description": "The target node id of the link"},
						"explanation": map[string]interface{}{"type": "string", "description": "An optional short label for the link describing what the relationship is"},
					},
					"required": []interface{}{"source", "target"},
				}},
			},
			"required": []interface{}{"nodes", "links"},
		},
		Callable: func(ctx context.Context, arguments map[string]interface{}) ([]mcp.CallToolResponseContent, error) {
			// Render template to buffer
			buffer := new(bytes.Buffer)
			if err := tmpl.Execute(buffer, arguments); err != nil {
				slog.Error("failed to execute graph template", slog.Any("err", err))
				return nil, fmt.Errorf("could not render graph html content: %w", err)
			}

			// Upload and get URL
			publicURL, err := renderAndUploadToMinio(ctx, buffer)
			if err != nil {
				return nil, err // Error already contains details
			}

			return []mcp.CallToolResponseContent{mcp.NewTextToolResponseContent("Graph rendered and uploaded: " + publicURL)}, nil
		},
	}
}
