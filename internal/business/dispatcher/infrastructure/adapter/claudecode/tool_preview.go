package claudecode

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

// toolPreviewLimit bounds one preview body or patch in Unicode code points,
// matching Claude Code's own Read limit of 262,144.
const toolPreviewLimit = 256 << 10

// toolResultFrame is the part of a native user frame the preview reads: the
// structured result Claude Code attaches beside the model-facing tool_result.
type toolResultFrame struct {
	ParentToolUseID string          `json:"parent_tool_use_id"`
	ToolUseResult   json.RawMessage `json:"tool_use_result"`
}

// nativeToolResult holds the fields of Claude Code 2.1.266's FileReadOutput,
// FileWriteOutput and FileEditOutput (sdk-tools.d.ts) a preview uses; the
// original file and git diff they also carry are never read.
type nativeToolResult struct {
	Type     string  `json:"type"`
	FilePath string  `json:"filePath"`
	Content  *string `json:"content"`
	File     *struct {
		FilePath            string  `json:"filePath"`
		Content             *string `json:"content"`
		NumLines            uint32  `json:"numLines"`
		StartLine           uint32  `json:"startLine"`
		TotalLines          uint32  `json:"totalLines"`
		TruncatedByTokenCap bool    `json:"truncatedByTokenCap"`
	} `json:"file"`
	OldString       *string `json:"oldString"`
	StructuredPatch []struct {
		OldStart uint32   `json:"oldStart"`
		OldLines uint32   `json:"oldLines"`
		NewStart uint32   `json:"newStart"`
		NewLines uint32   `json:"newLines"`
		Lines    []string `json:"lines"`
	} `json:"structuredPatch"`
}

// toolPreview takes the preview of the frame's one tool result. Claude Code
// attaches tool_use_result to the whole message, so it belongs to a
// tool_result only when the top-level message holds exactly one, without
// error, and the result is an object of a Read, Write or Edit shape; anything
// else keeps only the original output.
func toolPreview(raw []byte, blocks []userBody) *dispatcherpublicv1.ToolOutputPreview {
	var results int
	for _, block := range blocks {
		if block.Type == "tool_result" {
			results++
			if block.IsError {
				return nil
			}
		}
	}
	if results != 1 {
		return nil
	}
	var frame toolResultFrame
	if json.Unmarshal(raw, &frame) != nil || frame.ParentToolUseID != "" {
		return nil
	}
	if trimmed := skipWhitespace(frame.ToolUseResult); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var result nativeToolResult
	if json.Unmarshal(frame.ToolUseResult, &result) != nil {
		return nil
	}
	switch {
	case result.Type == "text" && result.File != nil && result.File.Content != nil:
		file := result.File
		preview := &dispatcherpublicv1.ToolOutputPreview{
			ToolName: "Read", FilePath: file.FilePath, StartLine: file.StartLine,
			NumLines: file.NumLines, TotalLines: file.TotalLines, TruncatedByTokenCap: file.TruncatedByTokenCap,
		}
		preview.Content, preview.Truncated, preview.TotalLength = cutText(*file.Content)
		return preview
	case (result.Type == "create" || result.Type == "update") && result.Content != nil:
		preview := &dispatcherpublicv1.ToolOutputPreview{ToolName: "Write", FilePath: result.FilePath}
		preview.Content, preview.Truncated, preview.TotalLength = cutText(*result.Content)
		return preview
	case result.Type == "" && result.OldString != nil && result.StructuredPatch != nil:
		preview := &dispatcherpublicv1.ToolOutputPreview{ToolName: "Edit", FilePath: result.FilePath}
		var used uint32
		for _, hunk := range result.StructuredPatch {
			size := patchHunkLength(hunk.Lines)
			preview.TotalLength += size
			// Hunks are kept or cut whole, so a shown hunk is never partial.
			if preview.Truncated || used+size > toolPreviewLimit {
				preview.Truncated = true
				continue
			}
			used += size
			preview.Hunks = append(preview.Hunks, &dispatcherpublicv1.PatchHunk{
				OldStart: hunk.OldStart, OldLines: hunk.OldLines, NewStart: hunk.NewStart, NewLines: hunk.NewLines, Lines: hunk.Lines,
			})
		}
		// A first hunk past the limit leaves nothing to show; the original
		// output then stands alone.
		if len(preview.Hunks) == 0 && preview.Truncated {
			return nil
		}
		return preview
	}
	return nil
}

// cutText keeps text within the preview limit, cutting at the last line
// break before it when there is one.
func cutText(text string) (kept string, truncated bool, total uint32) {
	total = uint32(utf8.RuneCountInString(text))
	if total <= toolPreviewLimit {
		return text, false, total
	}
	end, runes := 0, 0
	for index := range text {
		if runes == toolPreviewLimit {
			end = index
			break
		}
		runes++
	}
	kept = text[:end]
	if newline := strings.LastIndexByte(kept, '\n'); newline > 0 {
		kept = kept[:newline+1]
	}
	return kept, true, total
}

func patchHunkLength(lines []string) uint32 {
	var length uint32
	for _, line := range lines {
		length += uint32(utf8.RuneCountInString(line)) + 1
	}
	return length
}
