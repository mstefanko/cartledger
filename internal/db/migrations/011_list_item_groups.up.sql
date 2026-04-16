ALTER TABLE shopping_list_items
  ADD COLUMN product_group_id TEXT REFERENCES product_groups(id) ON DELETE SET NULL;
CREATE INDEX idx_shopping_list_items_group
  ON shopping_list_items(product_group_id)
  WHERE product_group_id IS NOT NULL;
