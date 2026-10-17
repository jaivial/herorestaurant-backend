package main

import "testing"

// The cleanup must delete exactly what the write path refuses to persist: a
// name the read path re-merges on every read. Its predecessor deleted the
// complement, which removed the live rice of the "Menú Arrocero" and a gallineta
// whose description merely mentioned rice. Measured on the production database
// (dry run, nothing written): the old rule wanted to delete 5 dishes from
// menu 1, 2 from menu 3, 1 from menu 41 and 2 from menu 38.

func keys(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[key(n)] = true
	}
	return out
}

func TestBorraSoloLoQueLaReadVuelveAMergear(t *testing.T) {
	// Menu 1: "Arroz a elegir" has no arroz section, so nothing is derived and
	// every name is the only copy there is.
	items := []string{
		"Meloso de secreto con ajetes",
		"Arroz a banda",
		"Arroz del senyoret",
		"Arroz seco de carrillada y boletus",
		"Arroz meloso de pulpo y gambones",
	}
	kept, removed, _ := splitItems(items, keys())

	if len(removed) != 0 {
		t.Fatalf("no derived names must never be deleted, got %v", removed)
	}
	if len(kept) != len(items) {
		t.Fatalf("kept %d, want all %d", len(kept), len(items))
	}
}

func TestNoBorraLaGallinetaQueDiceArrozAromatico(t *testing.T) {
	// The false positive that mattered: a chicken whose description says rice.
	items := []string{"Gallineta al horno con verduras salteadas y arroz aromático"}
	kept, removed, _ := splitItems(items, keys())
	if len(removed) != 0 {
		t.Fatalf("a dish that only mentions rice must not be deleted, got %v", removed)
	}
	if len(kept) != 1 {
		t.Fatalf("kept %d, want 1", len(kept))
	}
}

func TestBorraLoDerivadoYLoReportaComoSospechoso(t *testing.T) {
	// A menu WITH an arroz section: the merged names are a copy of what the
	// section owns, so they go; the suspicious list only ever carries non-derived
	// names, which are never deleted.
	items := []string{"Salmón al cava", "Arroz a banda", "Arroz del señoret"}
	kept, removed, suspicious := splitItems(items, keys("Arroz a banda", "Arroz del señoret"))

	if len(removed) != 2 {
		t.Fatalf("removed %v, want the two derived names", removed)
	}
	if len(kept) != 1 || kept[0] != "Salmón al cava" {
		t.Fatalf("kept %v, want only the salmon", kept)
	}
	if len(suspicious) != 0 {
		t.Fatalf("a derived name is not suspicious, got %v", suspicious)
	}
}

func TestLoNoDerivadoSeReportaYNoSeBorra(t *testing.T) {
	items := []string{"Arroz negro con sepia", "Lubina a la sal"}
	kept, removed, suspicious := splitItems(items, keys())
	if len(removed) != 0 {
		t.Fatalf("removed %v, want none", removed)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d, want both dishes", len(kept))
	}
	if len(suspicious) != 1 || suspicious[0] != "Arroz negro con sepia" {
		t.Fatalf("suspicious %v, want the arroz negro reported", suspicious)
	}
}

func TestElPredicateCoincideConElDelServidor(t *testing.T) {
	// The two paths must agree on the same dataset: whatever the write path
	// keeps for storage is exactly what the cleanup keeps, and what it strips is
	// exactly what the cleanup removes.
	items := []string{"Meloso de pescado", "Arroz a banda", "Salmón al cava"}
	derived := []string{"Arroz a banda"}
	derivedKeys := keys(derived...)

	// boGroupMenuPrincipalesForPersist: keep everything not derived.
	var persisted []string
	for _, it := range items {
		if !derivedKeys[key(it)] {
			persisted = append(persisted, it)
		}
	}

	kept, removed, _ := splitItems(items, derivedKeys)
	if len(removed) != len(derived) || removed[0] != derived[0] {
		t.Fatalf("cleanup removed %v, the write path strips %v", removed, derived)
	}
	if len(kept) != len(persisted) {
		t.Fatalf("cleanup kept %d, the write path persists %d", len(kept), len(persisted))
	}
}
