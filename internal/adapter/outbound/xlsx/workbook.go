// Package xlsx writes spreadsheets (Excel .xlsx) with excelize: tables with a bold grey header, thin borders,
// coloured cells where the model asks for them, the header row frozen and a filter on the first table
package xlsx

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"api/internal/core/port"
)

// Renderer implements port.WorkbookRenderer
type Renderer struct{}

func NewRenderer() *Renderer { return &Renderer{} }

var _ port.WorkbookRenderer = (*Renderer)(nil)

var border = []excelize.Border{{Type: "left", Color: "9A9A9A", Style: 1}, {Type: "right", Color: "9A9A9A", Style: 1}, {Type: "top", Color: "9A9A9A", Style: 1}, {Type: "bottom", Color: "9A9A9A", Style: 1}}

// styles caches one style per fill and weight
type styles struct {
	f     *excelize.File
	cache map[string]int
}

func (s *styles) get(fill *port.Colour, bold, header, title bool) (int, error) {
	key := fmt.Sprint(fill, bold, header, title)
	if id, ok := s.cache[key]; ok {
		return id, nil
	}
	st := &excelize.Style{Font: &excelize.Font{Family: "Calibri", Size: 10, Bold: bold || header}, Alignment: &excelize.Alignment{Vertical: "center", WrapText: true}}
	switch {
	case title:
		st.Font.Size, st.Font.Bold = 12, true
	default:
		st.Border = border
	}
	if header {
		st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"E9E9E9"}}
		st.Alignment.Horizontal = "center"
	} else if fill != nil {
		st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fmt.Sprintf("%02X%02X%02X", fill.R, fill.G, fill.B)}}
		st.Alignment.Horizontal = "center"
	}
	id, err := s.f.NewStyle(st)
	if err != nil {
		return 0, err
	}
	s.cache[key] = id
	return id, nil
}

// sheetName keeps a tab name within Excel's rules (31 characters, no []:*?/\)
func sheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 31 {
		s = string(r[:31])
	}
	return s
}

// Workbook writes every sheet and returns the .xlsx bytes
func (r *Renderer) Workbook(book port.Workbook) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	_ = f.SetDocProps(&excelize.DocProperties{Title: book.Title, Creator: "joseph"})
	st := &styles{f: f, cache: map[string]int{}}
	for i, sh := range book.Sheets {
		name := sheetName(sh.Name)
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				return nil, err
			}
		} else if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		for c, w := range sh.Widths {
			col, _ := excelize.ColumnNumberToName(c + 1)
			if err := f.SetColWidth(name, col, col, w); err != nil {
				return nil, err
			}
		}
		row := 1
		for ti, t := range sh.Tables {
			if t.Title != "" {
				id, err := st.get(nil, true, false, true)
				if err != nil {
					return nil, err
				}
				cell, _ := excelize.CoordinatesToCellName(1, row)
				_ = f.SetCellValue(name, cell, t.Title)
				_ = f.SetCellStyle(name, cell, cell, id)
				row++
			}
			headerRow := row
			if len(t.Columns) > 0 {
				id, err := st.get(nil, true, true, false)
				if err != nil {
					return nil, err
				}
				for c, h := range t.Columns {
					cell, _ := excelize.CoordinatesToCellName(c+1, row)
					_ = f.SetCellValue(name, cell, h)
					_ = f.SetCellStyle(name, cell, cell, id)
				}
				row++
			}
			for _, cells := range t.Rows {
				lines := 1
				for c, v := range cells {
					id, err := st.get(v.Fill, v.Bold, false, false)
					if err != nil {
						return nil, err
					}
					cell, _ := excelize.CoordinatesToCellName(c+1, row)
					_ = f.SetCellValue(name, cell, v.Text)
					_ = f.SetCellStyle(name, cell, cell, id)
					lines = max(lines, strings.Count(v.Text, "\n")+1)
				}
				if lines > 1 {
					_ = f.SetRowHeight(name, row, 13.5*float64(lines))
				}
				row++
			}
			if ti == 0 && sh.Filter && len(t.Columns) > 0 {
				first, _ := excelize.CoordinatesToCellName(1, headerRow)
				last, _ := excelize.CoordinatesToCellName(len(t.Columns), row-1)
				if err := f.AutoFilter(name, first+":"+last, nil); err != nil {
					return nil, err
				}
				// The header stays in view while scrolling
				_ = f.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: headerRow, TopLeftCell: fmt.Sprintf("A%d", headerRow+1), ActivePane: "bottomLeft"})
			}
			row++
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
