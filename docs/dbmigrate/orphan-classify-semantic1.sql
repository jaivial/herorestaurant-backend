-- orphan-classify-semantic1.sql (repair-semantic1). MANUAL read-only counts for
-- the coordinator. Aggregate counts only, no row values. Run on the PG scratch copy
-- (R5 restore) and on the MySQL source twin (same text works on both; no
-- dialect-specific syntax). Not executed by the worker. No mutation.

-- 1. Totals per FK group (expect 1 / 726 / 3 / 1 per the staging allowlist)
SELECT 'bo_invoice_comments' g, COUNT(*) n FROM bo_invoice_comments c WHERE NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id=c.invoice_id)
UNION ALL SELECT 'invoice_line_items', COUNT(*) FROM invoice_line_items l WHERE NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id=l.invoice_id)
UNION ALL SELECT 'site_builder_domain_mappings', COUNT(*) FROM site_builder_domain_mappings m WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=m.site_id)
UNION ALL SELECT 'site_builder_pages', COUNT(*) FROM site_builder_pages p WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=p.site_id);

-- 2. Distinct missing parent ids per group (one deleted parent vs many)
SELECT 'line_items_missing_invoices' g, COUNT(DISTINCT l.invoice_id) n FROM invoice_line_items l WHERE NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id=l.invoice_id)
UNION ALL SELECT 'comments_missing_invoices', COUNT(DISTINCT c.invoice_id) FROM bo_invoice_comments c WHERE NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id=c.invoice_id)
UNION ALL SELECT 'mappings_missing_sites', COUNT(DISTINCT m.site_id) FROM site_builder_domain_mappings m WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=m.site_id)
UNION ALL SELECT 'pages_missing_sites', COUNT(DISTINCT p.site_id) FROM site_builder_pages p WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=p.site_id);

-- 3. Missing invoice id above/below current max (gap = deleted vs never-existed id range)
SELECT 'line_items_parent_gt_max' g, COUNT(*) n FROM invoice_line_items l WHERE NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id=l.invoice_id) AND l.invoice_id > (SELECT COALESCE(MAX(id),0) FROM invoices)
UNION ALL SELECT 'comments_parent_gt_max', COUNT(*) FROM bo_invoice_comments c WHERE NOT EXISTS (SELECT 1 FROM invoices i WHERE i.id=c.invoice_id) AND c.invoice_id > (SELECT COALESCE(MAX(id),0) FROM invoices);

-- 4. Wrong-tenant/collision exposure: child has NO tenant column; only possible signal
--    is an orphan mapping domain also claimed elsewhere (count only).
SELECT 'orphan_mapping_domain_also_in_restaurant_domains' g, COUNT(*) n FROM site_builder_domain_mappings m WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=m.site_id) AND EXISTS (SELECT 1 FROM restaurant_domains d WHERE d.domain=m.domain)
UNION ALL SELECT 'orphan_mapping_domain_also_live_mapping', COUNT(*) FROM site_builder_domain_mappings m WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=m.site_id) AND EXISTS (SELECT 1 FROM site_builder_domain_mappings m2 JOIN site_builder_sites s2 ON s2.id=m2.site_id WHERE m2.domain=m.domain)
UNION ALL SELECT 'orphan_mapping_status_active_or_verified', COUNT(*) FROM site_builder_domain_mappings m WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=m.site_id) AND m.status IN ('active','verified')
UNION ALL SELECT 'orphan_page_is_home', COUNT(*) FROM site_builder_pages p WHERE NOT EXISTS (SELECT 1 FROM site_builder_sites s WHERE s.id=p.site_id) AND p.is_home=1;

-- 5. Restorable source check: are the missing parents present in any OTHER
--    available artifact (older dumps/backups)? Operator-only, outside SQL:
--    compare the distinct ids from (2) with older backup inventories. Never insert
--    reconstructed parents.
