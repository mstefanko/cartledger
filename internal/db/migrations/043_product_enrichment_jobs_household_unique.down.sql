DROP INDEX IF EXISTS idx_product_enrichment_jobs_active;

CREATE UNIQUE INDEX idx_product_enrichment_jobs_active
    ON product_enrichment_jobs(product_id, trigger, lookup_key)
    WHERE status IN ('queued', 'running');
