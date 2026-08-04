package api

import "testing"

func TestStockDocumentObjectPathIsTenantScopedAndOpaque(t *testing.T) {
	path := stockDocumentObjectPath(12, "invoice.pdf", "application/pdf")
	if path == "" || path[:19] != "stock-documents/12/" || path[len(path)-4:] != ".pdf" || path == "stock-documents/12/invoice.pdf" {
		t.Fatalf("unexpected private object path %q", path)
	}
}

func TestAccountingVATBucketsAllocateTicketDiscountExactly(t *testing.T) {
	got, err := accountingVATBuckets([]posAccountingLine{
		{ID: 1, GrossCents: 1100, VATRate: 10},
		{ID: 2, GrossCents: 1210, VATRate: 21},
	}, 310)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].GrossCents+got[1].GrossCents != 2000 {
		t.Fatalf("gross buckets do not balance: %+v", got)
	}
	for _, bucket := range got {
		if bucket.NetCents+bucket.TaxCents != bucket.GrossCents {
			t.Fatalf("VAT bucket does not balance: %+v", bucket)
		}
	}
}

func TestAccountingCSVCellNeutralizesFormula(t *testing.T) {
	if got := accountingCSVCell("=SUM(A1)"); got != "\"'=SUM(A1)\"" {
		t.Fatalf("got %s", got)
	}
}

func TestAccountingRefundVATBucketsBalanceExactly(t *testing.T) {
	got, err := accountingRefundVATBuckets([]posAccountingLine{{ID: 1, GrossCents: 1100, VATRate: 10}, {ID: 2, GrossCents: 1210, VATRate: 21}}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, bucket := range got {
		if bucket.NetCents+bucket.TaxCents != bucket.GrossCents {
			t.Fatalf("refund bucket does not balance: %+v", bucket)
		}
		total += bucket.GrossCents
	}
	if total != 1000 {
		t.Fatalf("refund total=%d", total)
	}
}

func TestProductionLabourAllocationRejectsOverAllocation(t *testing.T) {
	if err := validateProductionLabourAllocation(480, 300, 181); err == nil {
		t.Fatal("over-allocation accepted")
	}
	if err := validateProductionLabourAllocation(480, 300, 180); err != nil {
		t.Fatalf("valid allocation rejected: %v", err)
	}
}
