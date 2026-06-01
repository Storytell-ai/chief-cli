package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Storytell-ai/chief-go/chief"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

func newAssetsCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assets",
		Short: "Manage assets",
	}
	cmd.AddCommand(newAssetsUploadCommand(state))
	cmd.AddCommand(newAssetsListCommand(state))
	cmd.AddCommand(newAssetsGetCommand(state))
	cmd.AddCommand(newAssetsUpdateCommand(state))
	cmd.AddCommand(newDeleteCommand(state, "asset", func(ctx context.Context, id string) error {
		return state.chief.Assets.Delete(ctx, id)
	}))
	return cmd
}

// uploadResult is one file's outcome in --json mode. Skipped marks a file whose
// content already existed, so no bytes were sent.
type uploadResult struct {
	Path    string `json:"path"`
	AssetID string `json:"asset_id"`
	Status  string `json:"status"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newAssetsUploadCommand(state *app) *cobra.Command {
	var (
		recursive   bool
		concurrency int
		noWait      bool
		timeout     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file or directory of files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			files, rootIsDir, err := collectUploadFiles(args[0], recursive)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				return fmt.Errorf("no files to upload at %q", args[0])
			}
			if concurrency < 1 {
				concurrency = 1
			}
			return runUpload(cmd.Context(), state, args[0], rootIsDir, files, concurrency, !noWait, timeout)
		},
	}

	cmd.Flags().BoolVar(&recursive, "recursive", true, "descend into subdirectories")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "number of parallel uploads")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return after upload without waiting for ingest")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "per-file wait timeout")
	return cmd
}

// collectUploadFiles flattens the target into file paths, skipping dot-prefixed
// entries and, unless recursive, anything below the top level.
func collectUploadFiles(target string, recursive bool) ([]string, bool, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, false, fmt.Errorf("stat %q: %w", target, err)
	}
	if !info.IsDir() {
		return []string{target}, false, nil
	}

	var files []string
	err = filepath.WalkDir(target, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == target {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("walk %q: %w", target, err)
	}
	return files, true, nil
}

func runUpload(ctx context.Context, state *app, root string, rootIsDir bool, files []string, concurrency int, wait bool, timeout time.Duration) error {
	if !state.printer.json {
		state.printer.line(fmt.Sprintf("Uploading %d file(s)…", len(files)))
	}
	state.printer.startLive(len(files))

	results := make([]uploadResult, len(files))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			for i := range jobs {
				results[i] = uploadOne(ctx, state, root, rootIsDir, files[i], wait, timeout)
			}
		})
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	state.printer.stopLive()

	if state.printer.json {
		if err := state.printer.writeJSON(results); err != nil {
			return err
		}
	}

	failed, skipped := 0, 0
	for _, r := range results {
		switch {
		case r.Error != "" || r.Status == string(chief.AssetStatusFailed):
			failed++
		case r.Skipped:
			skipped++
		}
	}

	if !state.printer.json {
		state.printer.line(fmt.Sprintf("%d uploaded, %d skipped (already present), %d failed",
			len(files)-skipped-failed, skipped, failed))
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d uploads failed", failed, len(files))
	}
	return nil
}

// uploadOne renders its outcome immediately on a TTY. In JSON mode styled lines
// are suppressed, since output is deferred to the batch summary.
func uploadOne(ctx context.Context, state *app, root string, rootIsDir bool, path string, wait bool, timeout time.Duration) uploadResult {
	res := uploadResult{Path: path}
	name := filepath.Base(path)
	if rootIsDir {
		if rel, err := filepath.Rel(root, path); err == nil {
			name = rel
		}
	}

	var row *liveRow
	if !state.printer.json {
		row = state.printer.addRow(name)
	}

	asset, deduplicated, err := state.chief.Assets.UploadFile(ctx, path)
	switch {
	case err != nil:
		res.Error = err.Error()
	case deduplicated:
		res.Skipped = true
		res.AssetID = asset.AssetID
		res.Status = string(asset.Status)
	default:
		res.AssetID = asset.AssetID
		res.Status = string(asset.Status)
		if wait {
			state.printer.setRowState(row, "ingesting")
			asset, err = state.chief.Assets.WaitForReady(ctx, asset.AssetID, timeout)
			if asset != nil {
				res.AssetID = asset.AssetID
				res.Status = string(asset.Status)
			}
			if err != nil {
				res.Error = err.Error()
			}
		}
	}

	if !state.printer.json {
		p := state.printer
		var line string
		switch {
		case err != nil:
			line = fmt.Sprintf("%s %s %s", p.fail.Render("✗"), name, p.fail.Render(err.Error()))
		case res.Skipped:
			line = fmt.Sprintf("%s %s %s", p.skip.Render("•"), name, p.skip.Render("already uploaded"))
		default:
			detail := res.AssetID
			if res.Status != "" {
				detail = fmt.Sprintf("%s (%s)", res.AssetID, res.Status)
			}
			if detail != "" {
				line = fmt.Sprintf("%s %s %s", p.ok.Render("✓"), name, p.subtle.Render(detail))
			} else {
				line = fmt.Sprintf("%s %s", p.ok.Render("✓"), name)
			}
		}
		state.printer.finishRow(row, line)
	}
	return res
}

func newAssetsListCommand(state *app) *cobra.Command {
	f := &pagingFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List assets in the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			page, err := state.chief.Assets.List(cmd.Context(), f.options()...)
			if err != nil {
				return err
			}
			return state.printer.emit(page, func() { renderAssetTable(state.printer, page) })
		},
	}

	f.register(cmd, "asset", "assets")
	return cmd
}

func renderAssetTable(p *printer, page *chief.AssetPage) {
	if len(page.Data) == 0 {
		p.line("no assets")
		return
	}

	headers := []string{"ID", "STATUS", "FILENAME", "SIZE", "CREATED", "LABELS"}
	rows := make([][]string, 0, len(page.Data))
	for _, a := range page.Data {
		names := make([]string, len(a.Labels))
		for i, l := range a.Labels {
			names[i] = l.Name
		}
		rows = append(rows, []string{
			a.AssetID,
			string(a.Status),
			a.Filename,
			humanize.IBytes(uint64(a.SizeInBytes)),
			a.CreatedAt.Format(time.RFC3339),
			strings.Join(names, ", "),
		})
	}
	p.table(headers, rows)

	if page.HasMore {
		p.line(p.subtle.Render(fmt.Sprintf("more available (last_id %s)", page.LastID)))
	}
}

func newAssetsGetCommand(state *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a single asset by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			asset, err := state.chief.Assets.Get(cmd.Context(), args[0])
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("asset %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(asset, func() { printAssetDetail(state.printer, asset) })
		},
	}
	return cmd
}

func printAssetDetail(p *printer, asset *chief.Asset) {
	p.kv("Asset ID", asset.AssetID)
	p.kv("Status", string(asset.Status))
	p.kv("Filename", asset.Filename)
	p.kv("MIME type", asset.MimeType)
	p.kv("Size", humanize.IBytes(uint64(asset.SizeInBytes)))
	p.kv("Created", asset.CreatedAt.Format(time.RFC3339))
}

func newAssetsUpdateCommand(state *app) *cobra.Command {
	var (
		name        string
		description string
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an asset's metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &chief.UpdateAssetRequest{}
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("description") {
				req.Description = &description
			}
			asset, err := state.chief.Assets.Update(cmd.Context(), args[0], req)
			if err != nil {
				if chief.IsNotFound(err) {
					return fmt.Errorf("asset %q not found", args[0])
				}
				return err
			}
			return state.printer.emit(asset, func() { printAssetDetail(state.printer, asset) })
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	return cmd
}
