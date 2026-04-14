# UX Audit: Phase 2 Frontend UI Changes (Steps 6-7)

## User & Workflow

The user is a household member tracking grocery spending. They scan receipts, review extracted line items, browse product price history, and monitor spending on a dashboard. They use the app after shopping trips (often on mobile) and periodically to check trends and deals. The Phase 2 changes add store enrichment, payment tracking, receipt time, and sale/discount display across multiple pages.

---

## 7a. StoreViewPage -- Store Address & Store Number Display

### Current State
- Store header shows icon + name in a single `flex items-center gap-3` row (line 51-56).
- No secondary information below the header. Stats cards immediately follow at `mt-6`.

### Observed Pain Points

- [MEDIUM] **No visual grouping between store identity and new metadata.** The proposed address text (`text-body text-neutral-500 mt-1`) and store number badge sit directly below the name, but there is no containing element to visually group them. Every other content section on this page uses a white card with `rounded-2xl border border-neutral-200 bg-white shadow-subtle`. The header area has no card -- adding address and badge here creates an orphaned content cluster that does not match the page's card-based layout pattern.

- [LOW] **"Store #" prefix in the badge has an implicit assumption.** The work breakdown proposes `Store #{summary.store.store_number}` but the LLM prompt says the extracted value may already include "#" (e.g., "#749"). If the extracted value is "#749", the display would read "Store ##749". There is no normalization step described.

- [LOW] **Nickname field mentioned but not specified.** The work breakdown says "Add nickname editing support" but provides no UI specification. This is an unspecified interaction pattern -- inline editing does not exist anywhere else in the current app.

### Content Findings

- **Labeling:** "Store #749" is clear grocery terminology. "Location #749" would be less natural for grocery context. The proposed label is appropriate.
- **Nomenclature:** `store_number` is the internal field name. It does not leak into the UI as proposed -- the display uses "Store #" prefix. Acceptable.
- **Hierarchy:** Address as `text-neutral-500` (secondary) below the name (primary bold) is a correct content hierarchy -- name first, location details second.
- **Empty states:** The proposed code uses conditional rendering (`{summary.store.address && ...}`), so null values are handled by omission. This is correct -- showing nothing is better than showing a placeholder for address data.
- **Partial address concern:** The conditionals are per-field (`city &&`, `state &&`, `zip &&`), so a store with only a city but no state/zip would show "123 Main St, Springfield" with no state. This is a minor formatting edge case but could produce awkward-looking addresses.

### Sources
- `/Users/mstefanko/cartledger/web/src/pages/StoreViewPage.tsx` lines 44-56 -- current header structure
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 332-349 -- proposed 7a changes
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` line 87 -- LLM prompt store_number format includes "#"

---

## 7b. ReceiptReview -- Payment Badge & Time Display

### Current State
- The status bar (line 333-375) is a single flex row with: left side (matched/review badges + status text) and right side (buttons: View Original Receipt, View Raw JSON, Confirm All).
- The status bar is already dense with 3 buttons on the right and 2-3 badges on the left.
- Receipt date is NOT displayed in ReceiptReview itself -- it is shown by the parent page. ReceiptReview only shows line items and the status bar.

### Observed Pain Points

- [HIGH] **Status bar is already at capacity.** Adding payment badge and time to "after the status bar" (as proposed on line 353) requires careful placement. The current status bar has up to 3 items on the left (matched badge, review badge, status text) and 3 buttons on the right. On mobile viewports, this row already risks wrapping. Adding more content here increases the probability of layout breakage on small screens.

- [MEDIUM] **Receipt date is not shown in ReceiptReview.** The work breakdown proposes formatting `receipt_time` "alongside the date" (line 355), but ReceiptReview does not currently display the date at all. The date is presumably shown by the parent page (receipt detail page). The proposal does not specify where the date lives or whether the time should be added to ReceiptReview or to the parent. This is an unresolved placement question.

- [MEDIUM] **Payment badge format "Visa ...0388" has unaddressed variants.** The proposed format works for card payments but the LLM prompt allows: Visa, Mastercard, Amex, Discover, Debit, EBT, Cash, Check. For "Cash" and "Check" there is no last4 digits. The display format `{receipt.card_type} ...{receipt.card_last4}` would render as "Cash ...null" or "Cash ..." if last4 is null and the conditional is not tight enough. The work breakdown does not specify display for non-card payment methods.

- [LOW] **"..." vs standard masking convention.** Credit card masking typically uses a dot or bullet character, not three ASCII periods. Common patterns: "Visa ****0388" or "Visa ending in 0388". Using "...0388" with three dots is non-standard but not necessarily wrong -- it is a micro-convention choice that should be made intentionally.

### Content Findings

- **Labeling:** "Visa ...0388" communicates the essential information. Users of grocery receipt trackers would recognize this as payment method identification.
- **Progressive disclosure:** Payment method and time are secondary metadata. They should not compete with the primary content (line items table, match status). Placing them in a secondary info row below the status bar (rather than inside it) would be consistent with how the store view page separates stats from identity.
- **Hierarchy:** The current status bar serves an action-oriented purpose (confirm, view JSON). Payment and time are informational, not actionable. Mixing these in the same row conflates two purposes.

### Sources
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx` lines 330-375 -- current status bar structure
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 351-355 -- proposed 7b changes
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` line 87 -- payment_card_type enum values

---

## 7c. ReceiptReview -- Line Item Discount Display (Most Complex)

### Current State
- Price column (lines 295-299) renders a simple `$X.XX` in `tabular-nums` class.
- Column width is `size: 90` (90px).
- The column is editable (`meta: { editable: true, cellType: 'text' }`).
- The table uses `EditableTable` component with click-to-edit behavior.

### Observed Pain Points

- [HIGH] **Column width of 90px is insufficient for the proposed two-line discount display.** The proposal adds a second line with strikethrough original price + green savings amount. The content would be approximately: "$9.99" on line 1, "$14.99 -$5.00" on line 2. At 90px width with `text-small` font size, "$14.99 -$5.00" is approximately 13 characters. This will likely truncate or force wrapping within the second line, especially with the `ml-1` spacer between strikethrough and savings.

- [HIGH] **Interaction conflict with editable cells.** The Price column currently has `meta: { editable: true }`. The proposed discount display uses a `<div>` with nested `<span>` elements. When a user clicks this cell to edit, the editable table component needs to replace this complex display with an input. The work breakdown does not address how the edit interaction works with the two-line display -- does the user edit `total_price` only? Can they edit `regular_price` or `discount_amount`? The EditableTable component is not specified to handle cells that display different data than what they edit.

- [MEDIUM] **Strikethrough + green savings is a recognized grocery pattern, but density is high.** Grocery shoppers are familiar with seeing original price crossed out and a savings amount. This pattern appears on receipts, shelf tags, and in apps like Instacart and Target Circle. However, in a dense line-items table with 6+ columns and potentially 20+ rows, having some rows with single-line prices and others with two-line prices creates uneven row heights. This makes the table harder to scan vertically.

- [MEDIUM] **Edge case: large discounts.** If `regular_price` is "$149.99" and `discount_amount` is "$100.00", the second line content is "$149.99 -$100.00" (18 characters). At 90px this will certainly overflow. No maximum width or truncation strategy is specified.

- [LOW] **Color semantics.** The proposal uses `text-success-dark` for the savings amount (green). The existing Badge component uses `success` variant for positive things (matched items, best price, savings). Using green for discount amounts is consistent with the app's existing color vocabulary where green = good/savings.

- [LOW] **No accessibility annotation for strikethrough.** The `line-through` text decoration on the regular price has no `aria-label` or screen reader alternative. A screen reader would read "$14.99" without indicating it is a crossed-out/original price. The savings amount "-$5.00" also lacks context (is it a price or a discount?).

### Content Findings

- **Labeling:** No labels are added to the discount display -- it relies entirely on visual conventions (strikethrough = old price, green negative = savings). This is a common pattern in e-commerce but may not be universally understood without at least a tooltip.
- **Hierarchy:** Primary information is the paid price (`total_price`), secondary is the discount context. Placing `total_price` on the first line and discount details on a smaller second line is a correct hierarchy.
- **Nomenclature:** No internal terms leak. Dollar amounts with strikethrough and minus sign are self-explanatory.

### Patterns Found in Similar Products
- Instacart shows savings inline: paid price in bold, original price in strikethrough to the right, on the same line.
- Amazon shows the final price prominently, with a separate "List Price: $X.XX" and "You Save: $X.XX (XX%)" below it.
- Target Circle receipts show item price, then a negative line item below for the discount.
- None of these place the two-line discount pattern inside an editable table cell.

### Sources
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx` lines 288-300 -- current price column definition
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx` line 290 -- column size: 90
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 357-383 -- proposed 7c changes

---

## 7d. ReceiptsPage -- Payment & Time in Receipt List

### Current State
- Receipt list is a 4-column table: Store, Date, Total, Status.
- Table is inside a `border border-neutral-200 rounded-lg` container.
- Each row is 44px height with consistent cell padding.
- The table is minimal and scannable.

### Observed Pain Points

- [MEDIUM] **Adding columns increases horizontal width demands.** The current 4-column table fits comfortably on mobile (Store, Date, Total, Status). Adding payment and time columns would make 6 columns. On a 375px mobile viewport, 6 columns in a table will require horizontal scrolling or will force extremely narrow columns. The table container has `overflow-auto` so scrolling is possible, but hiding the Status or Total column behind a scroll defeats the purpose of a list view.

- [MEDIUM] **The work breakdown says "small visual additions" but does not specify the layout.** Line 388 says "Add payment badge and time to each receipt card in the list view" but the current list view is a TABLE, not cards. The phrase "receipt card" suggests either the author was thinking of a different layout or plans to convert the table to cards. This is an unresolved discrepancy.

- [LOW] **Time adds marginal value in a list view.** The receipt list is sorted by date. The primary use case for this list is "find a receipt from a particular shopping trip." Time (e.g., "2:15 PM") is rarely the distinguishing factor between receipts -- store name and date are. Time is useful on the detail page but may be noise in the list.

- [LOW] **Payment method adds marginal value in a list view.** Knowing you paid with "Visa ...0388" vs "Debit ...1234" is useful for reconciliation, but most users track one payment method per store. In a list of receipts, payment is not a primary differentiator. It would add more value as a filter rather than a displayed column.

### Content Findings

- **Progressive disclosure:** Payment and time are detail-level information. The current list shows the minimal set needed to identify and navigate to a receipt (store, date, amount, processing status). Adding detail-level fields to the list view goes against progressive disclosure -- show summary in lists, detail on detail pages.
- **Grouping/IA:** The current columns are grouped by: identification (Store, Date), financial (Total), and workflow (Status). Payment method is financial. Time is identification. If added, they should be adjacent to their semantic group.

### Sources
- `/Users/mstefanko/cartledger/web/src/pages/ReceiptsPage.tsx` lines 94-139 -- current table structure
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 386-389 -- proposed 7d changes

---

## 7e. ProductDetailPage -- Sale Savings Summary

### Current State
- `PriceTrendSection` (lines 39-87) shows: "Price Trend" heading, optional percent-change badge, bar chart sparkline, and stats row (Avg, Min, Max, purchase count).
- `TransactionsSection` (lines 493-535) shows a table with columns: Date, Store, Qty, Unit Price, Total.
- `ProductDetail.stats` currently has: `count`, `avg`, `min`, `max`. The work breakdown adds `total_saved`.

### Observed Pain Points

- [MEDIUM] **"You saved $X.XX" placement is unspecified beyond "in PriceTrendSection."** The stats row currently has 4 items in a flex row (Avg, Min, Max, count). Adding "You saved $X.XX" makes 5 items. On narrow viewports, this wraps and can look cluttered. However, the savings figure is arguably more actionable than Min/Max, so it may deserve more prominence than a stats row item.

- [LOW] **"You saved" is cumulative and potentially misleading.** If a user bought a product 50 times and it was on sale 3 times with total savings of $4.50, "You saved $4.50" out of context might seem insignificant or confusing. There is no indication of what time period or how many transactions this covers. Compare with "Saved $4.50 across 3 sale purchases" which provides context.

- [LOW] **TransactionsSection discount column/badge is unspecified.** The work breakdown says "add a column or badge for sale items showing the discount" (line 394) but does not specify which. A column adds width to an already 5-column table. A badge within an existing cell (e.g., appending a green badge to the Unit Price cell) would be more space-efficient.

### Content Findings

- **Labeling:** "You saved" is clear, personal language. It is consistent with grocery loyalty program language (Kroger, Safeway receipts say "You saved $X.XX today"). However, the app does not use "you" anywhere else -- all current labels are impersonal (e.g., "Total Spent", "Items Tracked", not "Your Total" or "Your Items"). Introducing "You saved" is a voice inconsistency.
- **Hierarchy:** Savings is a secondary stat. It should not visually compete with the primary trend chart or the Avg/Min/Max stats that drive purchase decisions.

### Sources
- `/Users/mstefanko/cartledger/web/src/pages/ProductDetailPage.tsx` lines 39-87 -- PriceTrendSection
- `/Users/mstefanko/cartledger/web/src/pages/ProductDetailPage.tsx` lines 493-535 -- TransactionsSection
- `/Users/mstefanko/cartledger/web/src/types/index.ts` lines 190-195 -- current stats shape
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 391-395 -- proposed 7e changes

---

## 7f. Sparkline Green Dots for Sale Data Points

### Current State
- `Sparkline` component (`web/src/components/ui/Sparkline.tsx`) renders an SVG polyline. It accepts `data: number[]` and renders a single-color line. There are no dots/circles on individual data points.
- `ProductDetailPage` `PriceTrendSection` (lines 62-76) uses a bar chart (not the Sparkline component) -- it renders `<div>` bars with `bg-brand` class.
- `AnalyticsPage` (line 92) uses the `<Sparkline>` component, passing `data` as `number[]` extracted from `SparklinePoint[]`.

### Observed Pain Points

- [HIGH] **The Sparkline component does not render individual data points.** It is a polyline only. Adding green dots for sale prices requires adding `<circle>` elements to the SVG for each data point where `is_sale` is true. However, the current `SparklineProps` accepts `data: number[]`, not `SparklinePoint[]`. The component would need a new prop (e.g., `highlights: boolean[]` or accept structured data) to know which points are sale prices. This is a component API change that is not specified in the work breakdown.

- [HIGH] **The ProductDetailPage does NOT use the Sparkline component.** It uses a custom bar chart (div-based). The work breakdown says to add green dots to "the sparkline/bar chart rendering in ProductDetailPage.tsx (line 62-76) and any DashboardPage.tsx sparkline components." The bar chart would need individual bars colored green rather than dots. The DashboardPage does not render any sparklines currently -- it shows overview cards, buy-again list, deals grid, and recent trips. There are no sparkline components on the dashboard.

- [MEDIUM] **A green dot without a legend is ambiguous.** Users would see some sparkline points colored green and others not. Without a legend or tooltip, the meaning of green is not self-evident. The app uses green elsewhere to mean: "matched" (checkmark), "savings" (badge), "price decrease" (trend badge), "best price" (store comparison). A green dot on a sparkline could be interpreted as any of these -- or as the cheapest price, not necessarily a sale price.

- [LOW] **Sparkline dimensions are small (80x24 default).** At this scale, colored dots are difficult to distinguish from the line itself. A 2-3px radius circle on a 24px-tall sparkline may be too subtle to notice, especially for users with color vision deficiencies.

### Content Findings

- **Progressive disclosure:** Sale indicators on sparklines are a "nice to know" enhancement. They do not change the primary information (price trend direction). This is additive detail appropriate for progressive disclosure -- but only if the meaning is discoverable.
- **Labeling:** No label or legend is proposed for the green dots. This is a gap. At minimum, a tooltip on hover showing "Sale price" would provide discoverability.

### Sources
- `/Users/mstefanko/cartledger/web/src/components/ui/Sparkline.tsx` lines 1-31 -- current component, polyline only, number[] input
- `/Users/mstefanko/cartledger/web/src/pages/ProductDetailPage.tsx` lines 62-76 -- bar chart, not Sparkline
- `/Users/mstefanko/cartledger/web/src/pages/DashboardPage.tsx` -- no sparkline components present
- `/Users/mstefanko/cartledger/web/src/pages/AnalyticsPage.tsx` lines 73, 92 -- Sparkline usage with number[] data
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 397-398 -- proposed 7f changes

---

## 7g. Analytics & Store API Type Updates

### Current State
- `StoreSummary` type (types/index.ts lines 544-552) includes `store: Store`. Since Store would be updated in Step 6, this flows through automatically.
- The work breakdown notes "store API functions need no changes since they already use the Store type."

### Observed Pain Points

- [LOW] **No UI changes are proposed for 7g.** This is a type-only update. The enriched store fields (address, city, state, zip, store_number) would be available in `StoreSummary` but are not proposed for display anywhere in the analytics views. This is correct for Phase 2 scope but means the analytics "Store Breakdown" cards would not show addresses even though the data is available. This is not a problem -- it is noted for completeness.

### Sources
- `/Users/mstefanko/cartledger/web/src/types/index.ts` lines 544-552 -- StoreSummary type
- `/Users/mstefanko/cartledger/WORK-BREAKDOWN-phase2.md` lines 400-403 -- proposed 7g changes

---

## Cross-Cutting Findings

### Empty States

- [MEDIUM] **Empty states are handled inconsistently across proposed changes.** 7a uses conditional rendering (null omission) for missing address/store_number -- good. 7b does not specify what to show when `card_type`/`card_last4` are null. 7c handles non-discount items by falling through to the simple price display -- good. 7e does not specify what to show when `total_saved` is null or zero. The work breakdown says "if total_saved is present and > 0" but does not say what happens otherwise (omit entirely? show "$0.00"?).

### Mobile / Responsive

- [HIGH] **No responsive considerations are specified for any change.** The current app uses Tailwind responsive prefixes (`sm:`, `lg:`) for grid layouts but the proposed additions do not mention responsive behavior. The receipt review status bar (7b), receipt list table columns (7d), and product detail stats row (7e) are all areas where added content would create problems on mobile viewports (375px width). The existing receipt list table already has `overflow-auto` but has no responsive column hiding.

### Tooltips / Help Text

- [MEDIUM] **No tooltips are proposed for any new element.** The sparkline green dots (7f) and the discount strikethrough pattern (7c) both rely on visual convention without explicit explanation. The existing app uses `title` attributes in exactly one place (the bar chart bars in PriceTrendSection, line 70). There is no tooltip component in the UI library.

### Label Consistency Across All Changes

- The app currently uses these terms consistently:
  - "Price" for monetary amounts (column headers, badges)
  - "Trips" for store visits (not "visits" or "transactions")
  - "Items" for line items (not "products" in list contexts)
  - "Matched/Unmatched" for line item status (not "linked/unlinked")
- The proposed changes do not introduce inconsistent terms. "Saved" and "Sale" are new vocabulary that do not conflict with existing terms.

### Existing Color Vocabulary

| Color | Current meaning | Proposed addition |
|-------|----------------|-------------------|
| Green (`success-dark`, `success-subtle`) | Matched, best price, savings badge, price decrease | Sale price indicator (7f), discount amount (7c) |
| Amber/Yellow (`warning`) | Needs review, pending | (none) |
| Red (`expensive`, `error`) | Price increase, errors, delete actions | (none) |
| Purple (`brand`) | Links, primary actions, sparkline color | (none) |
| Neutral gray | Secondary text, dividers | (none) |

Green is being used for an expanding set of meanings. Currently it means "positive outcome." Adding "sale price" to that set is semantically consistent (sales are positive for the buyer) but increases the ambiguity of what any specific green element means.

---

## Summary of Severity

| Severity | Count | Items |
|----------|-------|-------|
| HIGH | 5 | 7b status bar capacity, 7c column width, 7c edit interaction, 7f Sparkline API mismatch, responsive gaps |
| MEDIUM | 8 | 7a card grouping, 7b date location, 7b payment variants, 7d column count, 7d table-vs-card discrepancy, 7e savings placement, empty states, tooltip gaps |
| LOW | 9 | 7a store# prefix, 7a nickname, 7b masking convention, 7c accessibility, 7d time value, 7d payment value, 7e "You saved" voice, 7f dot size, 7g type-only |

---

## Confidence Notes

- The assessment of column width (90px for 7c) is based on character counting, not rendered measurement. Actual overflow behavior depends on the font metrics of the app's type scale (`text-small`), which was not measured.
- The claim that DashboardPage has no sparklines is based on reading the current file. If another component renders sparklines within the dashboard via a different path, this finding would be incorrect.
- Mobile breakpoint concerns are inferred from Tailwind responsive prefix usage patterns, not from testing on actual devices or viewport simulations.
- The observation that "You saved" introduces a voice inconsistency is a content tone finding that could be intentional. This may warrant human review if there is a content style guide.

## Status: COMPLETE
