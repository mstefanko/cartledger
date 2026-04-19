// Storage-zone tokens — the semantic palette + copy for Shopping Zones v1.
//
// Hex values MUST stay in lockstep with the `--color-zone-*` CSS variables in
// web/src/styles/tailwind.css. Tailwind 4 consumes those CSS vars to generate
// `bg-zone-produce`, `border-l-zone-cold`, etc. utilities — but downstream
// JS-driven render sites (the legend swatches, adaptive margin logic, aria
// labels, sort keys) read from this module so a palette change lands in both
// places from one commit.

export type StorageZone = 'produce' | 'cold' | 'frozen' | 'other'

export const ZONE_COLOR: Record<StorageZone, string> = {
  produce: '#10b981',
  cold: '#0891b2',
  frozen: '#4f46e5',
  other: '#9ca3af',
}

export const ZONE_LABEL: Record<StorageZone, string> = {
  produce: 'Produce',
  cold: 'Cold',
  frozen: 'Frozen',
  other: 'Other',
}

// The `other` description intentionally enumerates its constituents — per the
// plan §UI the user must see exactly what falls into the catch-all bucket so
// an item appearing under "Other" is never surprising.
export const ZONE_DESCRIPTION: Record<StorageZone, string> = {
  produce: 'Fresh fruits and vegetables.',
  cold: 'Refrigerated meat and dairy.',
  frozen: 'Freezer items.',
  other: 'Pantry, bakery, household, snacks, health, and more.',
}

export const ZONE_ORDER: Record<StorageZone, number> = {
  produce: 0,
  cold: 1,
  frozen: 2,
  other: 3,
}

export function zoneSortOrder(z: StorageZone): number {
  return ZONE_ORDER[z]
}

// Iteration order for the legend and any UI that renders all four zones.
// Mirrors ZONE_ORDER but is exposed as a typed array for ergonomics.
export const ZONES: StorageZone[] = ['produce', 'cold', 'frozen', 'other']
