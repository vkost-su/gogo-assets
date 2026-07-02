package sheets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	sheetsapi "google.golang.org/api/sheets/v4"

	"gogo-assets/internal/logging"
)

// ErrWrite is returned when a Sheets write or formatting batch fails.
var ErrWrite = errors.New("sheets: write failed")

const _scope = "https://www.googleapis.com/auth/spreadsheets"

// maxCellChars caps a single cell's content. Google Sheets rejects any cell over
// 50000 characters; we truncate below that (leaving room for the marker) so a
// pathological cell — e.g. a device with hundreds of installed apps — degrades to
// a truncated preview on the tab instead of failing the whole write. The full,
// untruncated data always remains in the run folder's JSON.
const maxCellChars = 49500

const _truncMarker = "… [truncated — see run-folder JSON]"

// clampCell truncates s to stay within the per-cell limit, cutting on a UTF-8
// rune boundary and appending a marker. len(s) (bytes) ≥ rune count, so a byte
// cap is a safe proxy for the character limit.
func clampCell(s string) string {
	if len(s) <= maxCellChars {
		return s
	}
	cut := maxCellChars - len(_truncMarker)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + _truncMarker
}

// Service wraps a sheets/v4 client + the configured spreadsheet ID.
type Service struct {
	srv           *sheetsapi.Service
	spreadsheetID string
}

// Open builds a Service authenticated with the given service-account JSON file.
//
// The SA must be shared with the spreadsheet as Editor.
func Open(ctx context.Context, saJSONPath, spreadsheetID string) (*Service, error) {
	creds, err := os.ReadFile(saJSONPath)
	if err != nil {
		return nil, fmt.Errorf("read SA JSON: %w", err)
	}
	cfg, err := google.JWTConfigFromJSON(creds, _scope)
	if err != nil {
		return nil, fmt.Errorf("parse SA JSON: %w", err)
	}
	srv, err := sheetsapi.NewService(ctx, option.WithTokenSource(cfg.TokenSource(ctx)))
	if err != nil {
		return nil, fmt.Errorf("build sheets service: %w", err)
	}
	return &Service{srv: srv, spreadsheetID: spreadsheetID}, nil
}

// WriteOptions tweaks the per-tab write behaviour.
type WriteOptions struct {
	// GrayRowHeader: when set, rows whose cell in this column equals "Yes"
	// are painted gray (used for "Suspended" rows).
	GrayRowHeader string
}

// writeTab is the inner workhorse: delete-and-recreate, write values, format.
func writeTab[T any](
	ctx context.Context,
	s *Service,
	name string,
	columns []Column[T],
	records []T,
	opts WriteOptions,
) error {
	log := logging.For("sheets")
	start := time.Now()
	ncols := len(columns)

	log.Info("writing tab", "name", name, "records", len(records))

	sheetID, err := s.recreateTab(ctx, name, max(2000, len(records)+10), ncols)
	if err != nil {
		return err
	}
	log.Info("recreated tab", "name", name, "rows", len(records)+10, "cols", ncols)

	groupRow := make([]any, ncols)
	headerRow := make([]any, ncols)
	for i, c := range columns {
		groupRow[i] = c.Group
		headerRow[i] = c.Header
	}

	dataRows := make([][]any, 0, len(records))
	stringRows := make([][]string, 0, len(records)) // kept for alert lookups
	for _, r := range records {
		row := make([]any, ncols)
		sRow := make([]string, ncols)
		for i, c := range columns {
			val := clampCell(safeExtract(c.Extract, r))
			row[i] = val
			sRow[i] = val
		}
		dataRows = append(dataRows, row)
		stringRows = append(stringRows, sRow)
	}

	footer := [][]any{
		make([]any, ncols),
		make([]any, ncols),
	}
	for i := range footer[0] {
		footer[0][i] = ""
		footer[1][i] = ""
	}
	footer[1][0] = fmt.Sprintf("Updated: %s", time.Now().UTC().Format("2006-01-02 15:04 UTC"))

	values := make([][]any, 0, 2+len(dataRows)+len(footer))
	values = append(values, groupRow, headerRow)
	values = append(values, dataRows...)
	values = append(values, footer...)

	if err := s.writeValues(ctx, name, values); err != nil {
		return fmt.Errorf("%w: values: %v", ErrWrite, err)
	}

	requests := []*sheetsapi.Request{}
	groups := make([]string, ncols)
	for i, c := range columns {
		groups[i] = c.Group
	}
	requests = append(requests, mergeGroupCells(sheetID, groups)...)
	requests = append(requests, fmtRow(sheetID, 0, 1, colorHeaderDark, true, true, "CENTER"))
	requests = append(requests, fmtRow(sheetID, 1, 2, colorHeaderLight, true, false, ""))
	requests = append(requests, freezeRows(sheetID, 2))
	requests = append(requests, autoResize(sheetID, ncols))
	for i, c := range columns {
		if c.Wrap {
			requests = append(requests, wrapColumn(sheetID, i))
		}
	}
	requests = append(requests, alertRequests(sheetID, stringRows, columns, opts.GrayRowHeader)...)

	if err := s.batchUpdate(ctx, requests); err != nil {
		return fmt.Errorf("%w: format: %v", ErrWrite, err)
	}

	log.Info("wrote tab",
		"name", name,
		"rows", len(records),
		"elapsed", logging.Elapsed(start))
	return nil
}

// recreateTab deletes the existing tab (if any) and creates a fresh one,
// returning its sheetId.
func (s *Service) recreateTab(ctx context.Context, name string, rows, cols int) (int64, error) {
	ss, err := s.srv.Spreadsheets.Get(s.spreadsheetID).Context(ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("get spreadsheet: %w", err)
	}

	var deleteReq []*sheetsapi.Request
	for _, sh := range ss.Sheets {
		if sh.Properties != nil && sh.Properties.Title == name {
			deleteReq = append(deleteReq, &sheetsapi.Request{
				DeleteSheet: &sheetsapi.DeleteSheetRequest{SheetId: sh.Properties.SheetId},
			})
			break
		}
	}
	addReq := &sheetsapi.Request{
		AddSheet: &sheetsapi.AddSheetRequest{
			Properties: &sheetsapi.SheetProperties{
				Title: name,
				GridProperties: &sheetsapi.GridProperties{
					RowCount:    int64(rows),
					ColumnCount: int64(cols),
				},
			},
		},
	}

	resp, err := s.srv.Spreadsheets.BatchUpdate(s.spreadsheetID, &sheetsapi.BatchUpdateSpreadsheetRequest{
		Requests: append(deleteReq, addReq),
	}).Context(ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("recreate tab %s: %w", name, err)
	}

	for _, r := range resp.Replies {
		if r.AddSheet != nil && r.AddSheet.Properties != nil {
			return r.AddSheet.Properties.SheetId, nil
		}
	}
	return 0, fmt.Errorf("recreate tab %s: AddSheet reply missing", name)
}

func (s *Service) writeValues(ctx context.Context, tab string, values [][]any) error {
	_, err := s.srv.Spreadsheets.Values.Update(s.spreadsheetID, tab+"!A1",
		&sheetsapi.ValueRange{Values: values}).
		ValueInputOption("RAW").
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("values update: %w", err)
	}
	return nil
}

func (s *Service) batchUpdate(ctx context.Context, requests []*sheetsapi.Request) error {
	if len(requests) == 0 {
		return nil
	}
	_, err := s.srv.Spreadsheets.BatchUpdate(s.spreadsheetID,
		&sheetsapi.BatchUpdateSpreadsheetRequest{Requests: requests}).
		Context(ctx).
		Do()
	return err
}

// alertRequests returns gray-row + cell-level alert requests for the data block.
func alertRequests[T any](
	sheetID int64,
	rows [][]string,
	columns []Column[T],
	grayRowHeader string,
) []*sheetsapi.Request {
	var out []*sheetsapi.Request

	grayCol := -1
	if grayRowHeader != "" {
		for i, c := range columns {
			if c.Header == grayRowHeader {
				grayCol = i
				break
			}
		}
	}

	for rowIdx, row := range rows {
		sheetRow := rowIdx + 2 // 0,1 are headers

		if grayCol >= 0 && row[grayCol] == "Yes" {
			out = append(out, rowColor(sheetID, sheetRow, colorRowGray))
		}
		for colIdx, c := range columns {
			value := row[colIdx]
			switch {
			case c.AlertRed != nil && c.AlertRed(value):
				out = append(out, cellColor(sheetID, sheetRow, colIdx, colorAlertRed))
			case c.AlertYellow != nil && c.AlertYellow(value):
				out = append(out, cellColor(sheetID, sheetRow, colIdx, colorAlertYellow))
			}
		}
	}
	return out
}

func safeExtract[T any](fn func(T) string, r T) (out string) {
	if fn == nil {
		return ""
	}
	defer func() {
		// Mirror the Python contract: extract MUST NOT raise. We defensively
		// recover and emit "" so one bad cell can't kill the whole sheet.
		if recover() != nil {
			out = ""
		}
	}()
	return fn(r)
}
