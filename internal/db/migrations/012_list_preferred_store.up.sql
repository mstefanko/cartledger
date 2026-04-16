ALTER TABLE shopping_lists
  ADD COLUMN preferred_store_id TEXT REFERENCES stores(id) ON DELETE SET NULL;
