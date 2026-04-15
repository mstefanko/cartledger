## UX Findings: Receipt Review Page

### User & Workflow
The user is a household member who scanned a grocery receipt. The system extracted line items via OCR/LLM and suggested product matches. The user now needs to review, correct, and confirm those matches so the data feeds into price tracking and budgeting.

### Observed Pain Points

- [HIGH] **Two-step accept/confirm flow is confusing with no visible effect between steps** -- The user clicks "Accept All Suggestions" and sees suggestions disappear from the Suggestion column, but the Product column still shows "Unmatched" for every row. Then clicking "Confirm All" only changes receipt status to "reviewed" with no visible change to line items. The user has no evidence that accepting actually worked. (ReceiptReview.tsx lines 438-449, 450-468; receipts.go lines 618-622)

- [HIGH] **Product column shows "Unmatched" even after suggestions are accepted and product_id is set** -- Root cause: the `getDisplayValue` callback (ReceiptReview.tsx lines 319-322) resolves product names via `productMap`, which is only populated from the product search query (lines 39-43, gated on `productSearch.length > 0`). After accepting suggestions, the backend sets `product_id` and the GET endpoint returns `product_name` via a JOIN (receipts.go lines 302-309), but `getDisplayValue` ignores `product_name` on the row and only checks the search-populated map. The fallback in `AutocompleteCell.tsx` line 278 renders "Unmatched" when `displayValue` is empty.

- [HIGH] **Post-confirmation state communicates nothing useful** -- After confirming, the only visible change is the status text switching to "Reviewed" and the "Confirm All" button becoming disabled with text "Confirmed". All rows still display "Unmatched". The user cannot distinguish a confirmed receipt from an unconfirmed one by looking at the line items. (ReceiptReview.tsx lines 460-467, 421-423)

- [MEDIUM] **Suggestion column empties after accept with no explanation** -- When suggestions are accepted, the Suggestion column goes blank (ReceiptReview.tsx line 288: renders null when `item.matched !== 'unmatched'`). The suggested names vanish but nothing replaces them. The user loses context about what was suggested. The backend clears `suggested_product_id` but not `suggested_name` (receipts.go line 620), creating an inconsistent data state.

- [MEDIUM] **"View Original Receipt" button is redundant** -- The receipt image is already visible in the left panel. The button scrolls to an element already on screen. The user correctly identified this as useless. (ReceiptReview.tsx lines 426-429; ReceiptReviewPage.tsx lines 106-129)

- [MEDIUM] **Receipt image panel wastes horizontal space** -- The layout is a 50/50 grid split (`lg:grid-cols-2`, ReceiptReviewPage.tsx line 104). Receipt paper is narrow; the photo includes dark background/table. The image gets `w-full` (line 118) so it stretches to fill half the viewport, with most of the width being non-receipt background. The data table -- which is the primary interaction surface -- is compressed into the other half.

- [LOW] **Status labels use inconsistent terminology** -- The receipt has statuses: `pending`, `matched`, `reviewed`, `processing`, `error` (types/index.ts line 86). Line items have: `unmatched`, `auto`, `manual`, `rule` (types/index.ts line 105). The status bar shows "matched" as a badge and also as a status label (ReceiptReview.tsx line 422). After confirming, it shows "Reviewed" -- but after page refresh it reverts to showing "matched" (line 422: only maps 'reviewed' explicitly, falls through to raw status for others). The terms "matched", "confirmed", and "reviewed" all appear to mean different things but the distinctions are not explained.

- [LOW] **"View Raw JSON" button is developer-facing, not user-facing** -- The modal title is "Raw LLM JSON" (ReceiptReview.tsx line 486), which exposes implementation details. This is a debug tool in the user's primary action bar.

### Gaps (what's missing vs. what users expect)

1. **Feedback gap after accepting suggestions**: The user expects to see product names populate in the Product column after accepting. Instead, the column remains "Unmatched" due to the frontend lookup bug. The backend correctly sets `product_id` and returns `product_name` in the API response, but the UI does not display it.

2. **Single-action confirmation vs. two-step flow**: The user expects one action to accept and finalize. The current flow requires: (a) Accept All Suggestions, (b) Confirm All. The distinction between these operations is not communicated -- "accept" creates products and sets matches in the database; "confirm" changes receipt status to "reviewed". Neither action produces visible feedback in the line items table.

3. **Missing post-accept state visualization**: After accepting, the icon column switches from sparkle to checkmark, but the Product column text remains "Unmatched". Users expect the product name to appear where "Unmatched" was.

4. **No undo or edit path after accepting**: Once suggestions are accepted, the Suggestion column is empty and there is no indication of what was matched. If a suggestion was wrong, the user has no way to see what was auto-matched or correct it inline (they would need to search for a product manually in the autocomplete).

5. **Receipt image not cropped or constrained**: The user expects the receipt image to show just the receipt, not the full photo with background. There is no image processing or smart cropping.

### Content Findings

- **Labeling:** "Accept All Suggestions" and "Confirm All" are ambiguous. "Accept" implies finality but does not complete the workflow. "Confirm" implies validation of something visible, but nothing has visibly changed. The badge counts ("17 matched", "17 suggested") update correctly after accepting, but the table rows contradict this by showing "Unmatched".

- **Nomenclature:** "Raw LLM JSON" in the modal title (line 486) leaks internal implementation. The `suggestion_type` values `existing_match` and `new_product` are used directly in badge text as "Match" and "New" (lines 289-290), which are abbreviated to the point of being unclear. The CSS class `text-expensive` (line 261) is a domain-specific semantic name that is fine internally but worth noting.

- **Grouping/IA:** The action bar mixes diagnostic tools ("View Original Receipt", "View Raw JSON") with primary workflow actions ("Accept All Suggestions", "Confirm All") at the same visual hierarchy level. All are secondary-styled buttons except "Confirm All". The diagnostic tools should be separated from the workflow actions.

- **Progressive disclosure:** All line items are shown at once with no filtering or grouping by match status. A receipt with 17 items is manageable, but there is no provision for longer receipts. The matched/suggested/unmatched badge counts in the header provide summary but there is no way to filter the table to show only items needing attention.

- **Hierarchy:** The status badges (matched count, suggested count) are the only summary. There is no progress indicator showing "X of Y items resolved" or a visual distinction between the accept step and the confirm step.

### Patterns Found in Similar Products

Not researched in this audit -- findings are based on source code analysis and user-reported screenshots only.

### Confidence Notes

1. The "Unmatched" persistence is a confirmed frontend bug: `getDisplayValue` does not use `product_name` from the row data, only from the search-populated `productMap`. The backend correctly returns product names. This is a code defect, not a design issue -- but it produces the primary UX pain point.

2. The status reversion from "Reviewed" to "matched" on page refresh (screenshot 3) suggests the `confirmReceipt` endpoint may not be persisting the status correctly, OR the `AcceptSuggestions` handler (line 675-681) overwrites the status back to "matched" when it detects all items are matched. This would need backend debugging to confirm the exact sequence.

3. The receipt image width concern is layout-level but the actual fix depends on whether images can be pre-processed (cropped) or whether CSS-only approaches suffice. The current `w-full` class on the img tag (ReceiptReviewPage.tsx line 118) is the direct cause.

### Sources
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx` -- Full component: accept/confirm flow, status bar, column definitions, getDisplayValue bug (lines 319-322), suggestion column render logic (line 288)
- `/Users/mstefanko/cartledger/web/src/pages/ReceiptReviewPage.tsx` -- Page layout: 50/50 grid (line 104), image rendering (lines 110-128), scroll-to-image handler
- `/Users/mstefanko/cartledger/web/src/api/receipts.ts` -- API functions: confirmReceipt only sets status to 'reviewed' (lines 55-62), acceptSuggestions sends line_item_ids (lines 41-49)
- `/Users/mstefanko/cartledger/internal/api/receipts.go` -- Backend: AcceptSuggestions sets product_id and matched='auto', clears suggested_product_id but not suggested_name (line 620); GET joins product names correctly (lines 302-310); suggestion_type computed only for unmatched items (lines 342-350); post-accept status check (lines 675-681)
- `/Users/mstefanko/cartledger/web/src/components/ui/EditableTable/AutocompleteCell.tsx` -- "Unmatched" fallback text (line 278)
- `/Users/mstefanko/cartledger/web/src/types/index.ts` -- Receipt status enum (line 86), LineItem matched enum (line 105), suggestion fields (lines 110-114)

## Status: COMPLETE
