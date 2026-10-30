package documentai

import (
	"testing"

	"cloud.google.com/go/documentai/apiv1/documentaipb"

	"github.com/avi-p/yesbook/internal/document"
)

func layout(start, end int64) *documentaipb.Document_Page_Layout {
	return &documentaipb.Document_Page_Layout{TextAnchor: &documentaipb.Document_TextAnchor{
		TextSegments: []*documentaipb.Document_TextAnchor_TextSegment{{StartIndex: start, EndIndex: end}},
	}}
}

func cell(start, end int64) *documentaipb.Document_Page_Table_TableCell {
	return &documentaipb.Document_Page_Table_TableCell{Layout: layout(start, end)}
}

func TestBlocksMapsPagesAndSkipsTableParagraphs(t *testing.T) {
	// Code point offsets:  0         10   15   20
	text := "שלום עולם\nשם   ציון\n"
	doc := &documentaipb.Document{
		Text: text,
		Pages: []*documentaipb.Document_Page{{
			PageNumber: 1,
			Paragraphs: []*documentaipb.Document_Page_Paragraph{
				{Layout: layout(0, 9)},
				{Layout: layout(10, 20)},
			},
			Tables: []*documentaipb.Document_Page_Table{{
				Layout:     layout(10, 20),
				HeaderRows: []*documentaipb.Document_Page_Table_TableRow{{Cells: []*documentaipb.Document_Page_Table_TableCell{cell(10, 12), cell(15, 19)}}},
			}},
		}},
	}

	got := blocks(doc, []int{7})
	if len(got) != 2 {
		t.Fatalf("blocks = %+v, want a table and one paragraph", got)
	}
	if got[0].Kind != document.BlockTable || got[0].Page != 7 || got[0].Rows[0][0] != "שם" || got[0].Rows[0][1] != "ציון" {
		t.Errorf("table = %+v", got[0])
	}
	if got[1].Text != "שלום עולם" || got[1].Page != 7 {
		t.Errorf("paragraph = %+v", got[1])
	}
}
