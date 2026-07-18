package yamlview

import (
	"strings"
	"testing"
)

func TestApplyManagedFieldsInsertsSummaryAfterMetadataWhenFolded(t *testing.T) {
	rendered := renderLines(fixtureLines(), map[string]bool{"managedFields": true})
	rendered = applyManagedFields(rendered, []string{"- manager: kubectl", "- manager: kubelet"}, map[string]bool{"managedFields": true})

	var metadataIdx, summaryIdx = -1, -1
	for i, rl := range rendered {
		if rl.Text == "metadata:" {
			metadataIdx = i
		}
		if rl.FoldKey == "managedFields" {
			summaryIdx = i
		}
	}
	if metadataIdx == -1 || summaryIdx == -1 {
		t.Fatalf("expected both metadata: and a managedFields fold summary, got %+v", rendered)
	}
	if summaryIdx != metadataIdx+1 {
		t.Fatalf("expected the summary immediately after metadata:, got metadata at %d, summary at %d", metadataIdx, summaryIdx)
	}
	if !strings.Contains(rendered[summaryIdx].Text, "2 lines folded") {
		t.Fatalf("expected the managedFields line count in the summary, got %q", rendered[summaryIdx].Text)
	}
	for _, rl := range rendered {
		if strings.Contains(rl.Text, "manager: kubectl") {
			t.Fatalf("expected managedFields content hidden while folded, got %+v", rendered)
		}
	}
}

func TestApplyManagedFieldsExpandsRealContentWhenUnfolded(t *testing.T) {
	rendered := renderLines(fixtureLines(), map[string]bool{})
	rendered = applyManagedFields(rendered, []string{"- manager: kubectl", "- manager: kubelet"}, map[string]bool{})

	var headerIdx = -1
	for i, rl := range rendered {
		if rl.FoldableKey == "managedFields" {
			headerIdx = i
		}
	}
	if headerIdx == -1 {
		t.Fatalf("expected an expanded managedFields header with FoldableKey set, got %+v", rendered)
	}
	if !strings.Contains(rendered[headerIdx+1].Text, "manager: kubectl") || !strings.Contains(rendered[headerIdx+2].Text, "manager: kubelet") {
		t.Fatalf("expected real managedFields content right after the header, got %+v", rendered)
	}
}

func TestApplyManagedFieldsOmitsInsertionWhenNoContent(t *testing.T) {
	rendered := renderLines(fixtureLines(), map[string]bool{})
	rendered = applyManagedFields(rendered, nil, map[string]bool{})
	for _, rl := range rendered {
		if rl.FoldableKey != "" || rl.FoldKey == "managedFields" {
			t.Fatalf("expected no managedFields insertion when there's no content, got %+v", rendered)
		}
	}
}

func TestSplitManagedFieldsLinesEmptyTextIsNil(t *testing.T) {
	if got := splitManagedFieldsLines(""); got != nil {
		t.Fatalf("splitManagedFieldsLines(\"\") = %+v, want nil", got)
	}
}

func TestSplitManagedFieldsLinesTrimsTrailingNewline(t *testing.T) {
	got := splitManagedFieldsLines("- manager: kubectl\n- manager: kubelet\n")
	want := []string{"- manager: kubectl", "- manager: kubelet"}
	if len(got) != len(want) {
		t.Fatalf("splitManagedFieldsLines = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitManagedFieldsLines[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
