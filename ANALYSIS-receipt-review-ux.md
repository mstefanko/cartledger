# Deep Analysis: Receipt Review UX Plan

## Assumptions Audit

| Assumption | Status |
|---|---|
| `getDisplayValue` only receives `value`, needs `rowIndex` | VERIFIED: `EditableTable.tsx:27` declares `getDisplayValue?: (value: TValue) => string`, called at line 133-134 with only `cell.getValue()` |
| Suggestion column exists and can be removed | VERIFIED: `ReceiptReview.tsx:292-316` defines a `suggestion` column with id='suggestion' |
| `acceptSuggestions` is transactional (all-or-nothing) | VERIFIED: `receipts.go:510-514` wraps entire operation in a single DB transaction with `defer tx.Rollback()` |
| AutocompleteCell shows "Unmatched" for empty values | VERIFIED: `AutocompleteCell.tsx:278` renders `{displayValue \|\| 'Unmatched'}` |
| Claude auto-downscales images >1568px long edge | UNVERIFIED: claimed in plan but not verifiable from code; matches public documentation |
| `bild` library is pure Go / no CGO | UNVERIFIED: claimed in plan, needs verification at implementation time |
| Image paths stored as comma-separated or JSON array | VERIFIED: `ReceiptReviewPage.tsx:44-53` parses both formats |
| No existing mobile collapse pattern for image panels | VERIFIED: grep for responsive patterns shows `sm:grid-cols` and `lg:grid-cols` usage across pages but no collapsible panel pattern anywhere in the codebase |

---

## Verification Summary

| # | Question | Answer | Verdict |
|---|----------|--------|---------|
| 1 | Does `getDisplayValue` actually need `rowIndex`? | No. The plan itself notes the alternative: "build a `Map<rowId, suggestedName>` and close over it." Since `getDisplayValue` receives `value` (the `product_id`), and for unmatched items `product_id` is null, you can't distinguish which row you're in from the value alone. However, the closure approach using a `Map<product_id \| null, suggestedName>` won't work either because multiple null rows exist. **You need either rowIndex or a different strategy.** | CONFIRMS plan is aware of the issue |
| 2 | Can accept + confirm really be atomic from the frontend? | `AcceptSuggestions` (receipts.go:510) runs in a single DB transaction. `confirmReceipt` is a separate PUT that sets status='reviewed'. The plan's `handleConfirm` calls accept then confirm sequentially. If accept succeeds but confirm fails, suggestions are accepted but receipt isn't marked reviewed -- this is recoverable (user clicks confirm again). If accept fails, nothing happens. **Not truly atomic but failure modes are benign.** | CONFIRMS -- acceptable risk |
| 3 | What happens to batch rule creation modal? | The plan's acceptance criteria mention it (line 88: "Batch rule creation modal still triggers if user manually matched items during review") but the proposed `handleConfirm` code snippet (lines 48-56) does NOT include the `pendingRuleMatches` check. Current code at `ReceiptReview.tsx:462-468` checks `pendingRuleMatches.length > 0` before confirming. | CONTRADICTS -- plan's code snippet is incomplete |
| 4 | Does removing the Suggestion column lose useful information? | The suggestion column shows a "Match"/"New" badge + suggested name. The plan moves the name into the Product column with amber/italic styling. The "Match" vs "New" distinction would be preserved via icon color (amber lightbulb vs blue lightbulb per the behavior matrix). **No information is lost.** | CONFIRMS |
| 5 | Does the plan handle PNG uploads? | `claude.go:119-133` `detectMediaType` handles JPEG, PNG, GIF, WebP. The plan's preprocessing pipeline (Phase 2) says "Decode JPEG" and "Re-encode as JPEG quality 85." PNGs would need `image/png` decode then JPEG re-encode. The plan doesn't mention PNG decode but `bild` handles both. The real gap: **re-encoding a PNG receipt as JPEG is fine, but the plan should explicitly handle the decode step for all supported formats, not just JPEG.** | NEUTRAL -- minor gap |
| 6 | Does the plan account for multi-page receipts? | `receipt.go:100-113` reads ALL files from the image directory into a `[][]byte` slice. The plan says to preprocess before passing to LLM but the code snippet (line 155) only mentions a single image. **The preprocessing must iterate over all images in the slice.** The plan's `PreprocessReceipt(raw []byte)` signature handles one image -- it should be called in a loop, which is trivial but not stated. | NEUTRAL -- trivial to address |

---

## Recommended Approach

Execute the plan as written, with the fixes below. The plan is well-structured and the phasing (UX first, layout second, backend last) is correct.

### Why Not "Keep Two Buttons"

The strongest argument for keeping separate Accept/Confirm is explicitness -- the user knows exactly what each action does. However, the current UX testing showed users don't understand the difference, and the two-step flow provides no meaningful rollback opportunity (there's no "undo accept" action). A single confirmation with inline suggestion display is strictly better.

---

## Contradictions Found & How Resolved

1. **Batch rule modal missing from `handleConfirm` snippet**: The plan's pseudocode for `handleConfirm` (lines 48-56) skips the `pendingRuleMatches` check that currently gates the confirm flow. The implementation MUST preserve the existing logic at `ReceiptReview.tsx:462-468`: check for pending rule matches, show the batch rule modal if any exist, and only call confirm after the modal resolves.

2. **`getDisplayValue` approach**: The plan correctly identifies the problem but presents two options without deciding. **Recommendation: use the closure approach with a `Map<lineItemId, suggestedDisplayName>` keyed by line item ID, not product_id.** However, `getDisplayValue` only receives the cell value (product_id), not the row ID. So either:
   - **(A)** Extend `getDisplayValue` signature to `(value: TValue, rowIndex: number) => string` -- requires changing EditableTable.tsx:27 and the call site at line 133. Simple, one-line type change + one-line call-site change.
   - **(B)** Don't use `getDisplayValue` for suggestions at all. Instead, use a custom `cell` renderer on the product column (like the status column already does) that has access to `row.original`. This avoids touching the generic EditableTable contract.

   **Recommendation: Option B.** It's more localized -- the Product column already needs custom rendering for amber/italic styling, which `getDisplayValue` (returning a plain string) can't express anyway. You'll need a custom cell renderer regardless, so using `getDisplayValue` for suggestions is redundant.

---

## Work Breakdown

### Phase 1: Review Flow UX

1. **Remove suggestion column** in `ReceiptReview.tsx:292-316` -- delete the entire column definition with id='suggestion'.

2. **Add custom cell renderer to product column** in `ReceiptReview.tsx:318-334` -- replace the simple `meta.getDisplayValue` approach with a `cell` property that:
   - Reads `row.original` to check `suggestion_type`, `suggested_product_name`, `suggested_name`
   - Renders matched products in normal text
   - Renders suggestions in amber/italic (existing match) or blue/italic (new product)
   - Renders "Unmatched" in gray italic for items with no suggestion
   - NOTE: Must still support clicking to open the autocomplete typeahead. Verify that a custom `cell` renderer works alongside `meta.cellType: 'autocomplete'` in EditableTable -- currently EditableTable.tsx:132 checks `meta?.editable && meta.cellType === 'autocomplete'` and renders AutocompleteCell, which takes precedence over `cell`. **This means the custom cell renderer will be ignored for autocomplete columns.** The suggestion styling must go into AutocompleteCell itself via a new prop (e.g., `suggestedDisplayValue` and `suggestionStyle`).

3. **Modify AutocompleteCell** in `AutocompleteCell.tsx:264-282` -- add a `suggestedDisplayValue?: string` and `suggestionVariant?: 'existing' | 'new'` prop. When `!value && suggestedDisplayValue`, render the suggestion text with appropriate styling instead of "Unmatched". The props interface is at line 16-29.

4. **Remove acceptMutation and Accept All button** in `ReceiptReview.tsx:142-151` and `447-457`.

5. **Merge accept into confirm flow** in `ReceiptReview.tsx:459-477` -- modify the confirm button handler to:
   - Check for `suggestedRows.length > 0`, if so call `acceptSuggestions` first
   - THEN check for `pendingRuleMatches.length > 0` (preserve existing batch rule modal flow)
   - THEN call `confirmReceipt`
   - Add loading state that covers the full sequence

6. **Remove "View Original Receipt" button** in `ReceiptReview.tsx:435-439`.

7. **Shrink "View Raw JSON" button** in `ReceiptReview.tsx:440-446` -- replace with an icon button.

### Phase 3: Layout (do second, pairs with Phase 1)

8. **Change grid** in `ReceiptReviewPage.tsx:104` -- from `lg:grid-cols-2` to `lg:grid-cols-[1fr_2fr]`.

9. **Constrain image width** in `ReceiptReviewPage.tsx:111-121` -- add `max-w-[280px] mx-auto` to the image container.

10. **Mobile collapse** -- this is a NEW pattern not found elsewhere in the codebase. Simplest approach: on mobile (`lg:` breakpoint), hide the image section by default with a small thumbnail + "Show receipt image" toggle. Use standard React state + Tailwind `hidden lg:block` pattern. Do not introduce a generic collapsible component for this single use case.

### Phase 2: Image Preprocessing (do last)

11. **Create `internal/imaging/preprocess.go`** with `PreprocessReceipt(raw []byte) ([]byte, error)`. Must handle JPEG, PNG, GIF, WebP decode (match the formats in `claude.go:119-133`). Always re-encode as JPEG.

12. **Integrate in `receipt.go:105-109`** -- after `os.ReadFile`, call `PreprocessReceipt` on each image in the loop. Store preprocessed bytes in the `images` slice sent to LLM. Optionally write preprocessed image back to disk for frontend display.

13. **Add `bild` dependency** to `go.mod`.

---

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| AutocompleteCell custom cell renderer ignored by EditableTable | HIGH | Verified: EditableTable.tsx:132 renders AutocompleteCell when cellType='autocomplete', skipping any custom `cell` property. Suggestion styling MUST go through AutocompleteCell props, not a custom cell renderer. Work item 2 updated accordingly. |
| Confirm button doing accept+confirm is not truly atomic | LOW | Accept is transactional server-side. If confirm fails after accept succeeds, user just clicks confirm again. No data corruption possible. |
| Batch rule modal regression | MEDIUM | The plan's code snippet omits the pendingRuleMatches check. Writer must preserve the existing modal trigger logic from ReceiptReview.tsx:462-468. |
| `bild` library purity (no CGO) | LOW | Verify at `go get` time. If CGO needed, fall back to `disintegration/imaging` which is confirmed pure Go. |
| Bounding-box crop on varied backgrounds | MEDIUM | Receipts on white tables, curved edges, fingers in frame will all confuse threshold-based cropping. The plan's "skip if no clear boundary" fallback is correct but needs a confidence threshold. Recommend: if bounding box covers >90% of image area, skip the crop entirely. |
| Disk space for dual storage (original + preprocessed) | LOW | 5MB original + 500KB preprocessed = 5.5MB per receipt. At 100 receipts/month = 550MB/month. Acceptable for a household app. |
| No error handling for preprocessing failure | MEDIUM | If preprocessing throws (corrupt image, OOM on large image), it should fall back to sending the raw image, not fail the entire scan. Add `log.Printf` warning and continue with raw bytes. |

---

## Gaps Not Addressed in Plan

1. **No backend API changes mentioned, but none needed** -- confirmed. `acceptSuggestions` and `confirmReceipt` already exist as separate endpoints. The frontend just calls them in sequence.

2. **No testing strategy** -- the plan has no mention of tests. Recommend:
   - Unit test for `PreprocessReceipt` with sample JPEG/PNG inputs
   - Integration test: scan receipt with preprocessing, verify extraction still works
   - Manual regression: verify batch rule modal still triggers after the confirm flow merge

3. **Backwards compatibility for existing receipts** -- existing receipts with `suggestion_type` data will work fine with the new UI (the data model is unchanged). Existing receipts without preprocessed images will display their original images (no migration needed).

4. **`suggestedRows` filter may need updating** -- `ReceiptReview.tsx:132` defines `suggestedRows` as items where `matched === 'unmatched' && suggestion_type != null`. If a user manually matches one suggested item via the typeahead (changing its `matched` status), it correctly drops out of `suggestedRows`. When confirm is clicked, only remaining unresolved suggestions are sent to `acceptSuggestions`. This works correctly.

5. **Loading/error state during combined accept+confirm** -- the plan doesn't address UX during the multi-step confirm. Recommend a single `isConfirming` state that disables the button and shows "Confirming..." for the entire sequence.

---

## Open Questions (unresolved UNKNOWNs)

1. **Claude's actual token behavior with pre-resized images** -- the plan claims same token count for 1568px pre-resize vs letting Claude auto-downscale. This matches public docs but actual savings come from reduced upload bandwidth and faster TTFT, not token reduction. Worth A/B testing with real receipts.

2. **`bild` CGO status** -- needs verification at implementation time. The README says pure Go but some image operations in Go ecosystem quietly pull in CGO for performance.

---

## Sources

- `ReceiptReview.tsx` -- full component, 594 lines, contains acceptMutation, confirmMutation, columns, batch rule modal
- `AutocompleteCell.tsx:264-282` -- display rendering, "Unmatched" fallback at line 278
- `EditableTable.tsx:27` -- `getDisplayValue` type signature, `(value: TValue) => string`
- `EditableTable.tsx:132-134` -- autocomplete cell rendering precedence (overrides custom `cell`)
- `receipts.go:482-600` -- AcceptSuggestions handler, transactional, handles both existing match and new product creation
- `receipt.go:100-113` -- image reading loop, multi-image support
- `claude.go:49-52` -- image encoding, base64, media type detection
- `claude.go:119-133` -- detectMediaType supports JPEG, PNG, GIF, WebP
- `ReceiptReviewPage.tsx:104` -- current `lg:grid-cols-2` layout
- `types/index.ts:105-114` -- LineItem type with suggestion fields

---

## Confidence: HIGH (85%)

The plan is solid and well-researched. The main risk is the AutocompleteCell rendering interaction (Risk #1 above) which would cause a false start if not caught. All other gaps are minor and addressable during implementation.

## Status: COMPLETE

## Handoff: Option C (standalone)

This analysis is sufficient for a writer to execute directly. No further decomposition needed -- the work breakdown above is ordered by dependency and specific to file:line regions.
