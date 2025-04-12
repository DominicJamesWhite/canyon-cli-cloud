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

	"cloud.google.com/go/storage" // Added for GCS client
	"github.com/Masterminds/sprig/v3"
	// "github.com/pkg/browser" // No longer needed

	"github.com/humanitec/canyon-cli/internal/mcp"
)

const (
	gcsBucketName = "canyon-demo-html-renders" // Updated bucket name
	gcsPathPrefix = "canyon-renders"           // As specified by user
	gcsBaseURL    = "https://storage.googleapis.com"
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

// renderAndUploadToGCS takes the rendered HTML buffer, uploads it to GCS using the Go client,
// and returns a signed URL for temporary access.
func renderAndUploadToGCS(ctx context.Context, buffer *bytes.Buffer) (string, error) {
	// 1. Initialize GCS client
	client, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer client.Close()

	// 2. Generate filename and object path
	filename := generateRandomFilename()
	objectPath := fmt.Sprintf("%s/%s", gcsPathPrefix, filename) // Path within the bucket

	// 3. Get bucket handle and object handle
	bucket := client.Bucket(gcsBucketName)
	obj := bucket.Object(objectPath)

	// 4. Upload the content using a Writer
	wc := obj.NewWriter(ctx)
	wc.ContentType = "text/html" // Set content type for proper browser rendering
	// Consider adding Cache-Control headers if needed: wc.CacheControl = "public, max-age=..."

	if _, err = wc.Write(buffer.Bytes()); err != nil {
		return "", fmt.Errorf("failed to write data to GCS object writer: %w", err)
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("failed to close GCS object writer: %w", err)
	}
	slog.Info("Successfully uploaded to GCS", slog.String("bucket", gcsBucketName), slog.String("object", objectPath))

	// 5. Generate a signed URL (valid for 15 minutes)
	// 5. Generate a signed URL (valid for 15 minutes)
	// Use VirtualHostedStyle to avoid potential "host" header issues with V4 signing.
	opts := &storage.SignedURLOptions{
		Scheme:             storage.SigningSchemeV4,
		Method:             "GET",
		Expires:            time.Now().Add(15 * time.Minute),
		VirtualHostedStyle: true, // Add this line
	}

	signedURL, err := client.Bucket(gcsBucketName).SignedURL(objectPath, opts)
	if err != nil {
		return "", fmt.Errorf("failed to generate signed URL: %w", err)
	}
	slog.Info("Generated signed URL", slog.String("url", signedURL)) // Log the URL for debugging if needed

	return signedURL, nil
}

// NewRenderCSVAsTable renders csv as a table and uploads to GCS.
func NewRenderCSVAsTable() mcp.Tool {
	tmpl, err := template.New("").Funcs(funcMap).Parse(renderCsvTemplate)
	if err != nil {
		panic(err)
	}
	return mcp.Tool{
		Name:        "render_csv_as_table_to_gcs",
		Description: `This tool renders CSV data as an HTML table and uploads it to Google Cloud Storage, returning a public link.`,
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
			publicURL, err := renderAndUploadToGCS(ctx, buffer)
			if err != nil {
				return nil, err // Error already contains details
			}

			return []mcp.CallToolResponseContent{mcp.NewTextToolResponseContent("CSV rendered and uploaded: " + publicURL)}, nil
		},
	}
}

// NewRenderTreeAsTree renders a hierarchy and uploads to GCS.
func NewRenderTreeAsTree() mcp.Tool {
	tmpl, err := template.New("").Funcs(funcMap).Parse(renderTreeTemplate)
	if err != nil {
		panic(err)
	}
	return mcp.Tool{
		Name:        "render_data_as_tree_to_gcs",
		Description: `This tool renders hierarchical data (like a tree structure) as HTML and uploads it to Google Cloud Storage, returning a public link.`,
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
			publicURL, err := renderAndUploadToGCS(ctx, buffer)
			if err != nil {
				return nil, err // Error already contains details
			}

			return []mcp.CallToolResponseContent{mcp.NewTextToolResponseContent("Tree rendered and uploaded: " + publicURL)}, nil
		},
	}
}

// NewRenderNetworkAsGraph renders a network graph and uploads to GCS.
func NewRenderNetworkAsGraph() mcp.Tool {
	tmpl, err := template.New("").Funcs(funcMap).Parse(renderGraphTemplate)
	if err != nil {
		panic(err)
	}
	return mcp.Tool{
		Name:        "render_network_as_graph_to_gcs",
		Description: `This tool renders an interconnected network as a force-directed graph in HTML and uploads it to Google Cloud Storage, returning a public link.`,
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
			publicURL, err := renderAndUploadToGCS(ctx, buffer)
			if err != nil {
				return nil, err // Error already contains details
			}

			return []mcp.CallToolResponseContent{mcp.NewTextToolResponseContent("Graph rendered and uploaded: " + publicURL)}, nil
		},
	}
}
