package sheets

import (
	sheetsapi "google.golang.org/api/sheets/v4"
)

// Colour palette mirrors the Python writer (Sheets API uses 0..1 floats).
var (
	colorHeaderDark  = &sheetsapi.Color{Red: 0.176, Green: 0.290, Blue: 0.431}
	colorHeaderLight = &sheetsapi.Color{Red: 0.910, Green: 0.930, Blue: 0.940}
	colorAlertRed    = &sheetsapi.Color{Red: 0.957, Green: 0.800, Blue: 0.800}
	colorAlertYellow = &sheetsapi.Color{Red: 1.000, Green: 0.950, Blue: 0.800}
	colorRowGray     = &sheetsapi.Color{Red: 0.900, Green: 0.900, Blue: 0.900}
	colorWhite       = &sheetsapi.Color{Red: 1, Green: 1, Blue: 1}
)

// mergeGroupCells returns one mergeCells request per consecutive same-group
// run in row 0. Single-column groups are not merged.
func mergeGroupCells(sheetID int64, groups []string) []*sheetsapi.Request {
	var out []*sheetsapi.Request
	i := 0
	for i < len(groups) {
		j := i + 1
		for j < len(groups) && groups[j] == groups[i] {
			j++
		}
		if j-i > 1 {
			out = append(out, &sheetsapi.Request{
				MergeCells: &sheetsapi.MergeCellsRequest{
					Range: &sheetsapi.GridRange{
						SheetId:          sheetID,
						StartRowIndex:    0,
						EndRowIndex:      1,
						StartColumnIndex: int64(i),
						EndColumnIndex:   int64(j),
					},
					MergeType: "MERGE_ALL",
				},
			})
		}
		i = j
	}
	return out
}

// fmtRow paints a header row with background, bold text, optional white
// foreground, and optional horizontal alignment.
func fmtRow(sheetID int64, start, end int64, bg *sheetsapi.Color, bold, whiteText bool, align string) *sheetsapi.Request {
	tf := &sheetsapi.TextFormat{Bold: bold}
	if whiteText {
		tf.ForegroundColor = colorWhite
	}
	fmt := &sheetsapi.CellFormat{
		BackgroundColor: bg,
		TextFormat:      tf,
	}
	fields := "userEnteredFormat(backgroundColor,textFormat)"
	if align != "" {
		fmt.HorizontalAlignment = align
		fields = "userEnteredFormat(backgroundColor,textFormat,horizontalAlignment)"
	}
	return &sheetsapi.Request{
		RepeatCell: &sheetsapi.RepeatCellRequest{
			Range:  &sheetsapi.GridRange{SheetId: sheetID, StartRowIndex: start, EndRowIndex: end},
			Cell:   &sheetsapi.CellData{UserEnteredFormat: fmt},
			Fields: fields,
		},
	}
}

func cellColor(sheetID int64, row, col int, c *sheetsapi.Color) *sheetsapi.Request {
	return &sheetsapi.Request{
		RepeatCell: &sheetsapi.RepeatCellRequest{
			Range: &sheetsapi.GridRange{
				SheetId:          sheetID,
				StartRowIndex:    int64(row),
				EndRowIndex:      int64(row + 1),
				StartColumnIndex: int64(col),
				EndColumnIndex:   int64(col + 1),
			},
			Cell:   &sheetsapi.CellData{UserEnteredFormat: &sheetsapi.CellFormat{BackgroundColor: c}},
			Fields: "userEnteredFormat.backgroundColor",
		},
	}
}

func rowColor(sheetID int64, row int, c *sheetsapi.Color) *sheetsapi.Request {
	return &sheetsapi.Request{
		RepeatCell: &sheetsapi.RepeatCellRequest{
			Range: &sheetsapi.GridRange{
				SheetId:       sheetID,
				StartRowIndex: int64(row),
				EndRowIndex:   int64(row + 1),
			},
			Cell:   &sheetsapi.CellData{UserEnteredFormat: &sheetsapi.CellFormat{BackgroundColor: c}},
			Fields: "userEnteredFormat.backgroundColor",
		},
	}
}

func freezeRows(sheetID int64, count int64) *sheetsapi.Request {
	return &sheetsapi.Request{
		UpdateSheetProperties: &sheetsapi.UpdateSheetPropertiesRequest{
			Properties: &sheetsapi.SheetProperties{
				SheetId:        sheetID,
				GridProperties: &sheetsapi.GridProperties{FrozenRowCount: count},
			},
			Fields: "gridProperties.frozenRowCount",
		},
	}
}

func wrapColumn(sheetID int64, col int) *sheetsapi.Request {
	return &sheetsapi.Request{
		RepeatCell: &sheetsapi.RepeatCellRequest{
			Range: &sheetsapi.GridRange{
				SheetId:          sheetID,
				StartRowIndex:    2,
				StartColumnIndex: int64(col),
				EndColumnIndex:   int64(col + 1),
			},
			Cell: &sheetsapi.CellData{UserEnteredFormat: &sheetsapi.CellFormat{
				WrapStrategy:      "WRAP",
				VerticalAlignment: "TOP",
			}},
			Fields: "userEnteredFormat(wrapStrategy,verticalAlignment)",
		},
	}
}

func autoResize(sheetID int64, ncols int) *sheetsapi.Request {
	return &sheetsapi.Request{
		AutoResizeDimensions: &sheetsapi.AutoResizeDimensionsRequest{
			Dimensions: &sheetsapi.DimensionRange{
				SheetId:    sheetID,
				Dimension:  "COLUMNS",
				StartIndex: 0,
				EndIndex:   int64(ncols),
			},
		},
	}
}
