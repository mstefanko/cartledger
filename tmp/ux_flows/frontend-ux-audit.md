## UX Findings: CartLedger Frontend Audit

### User & Workflow
Two users (Mike and wife) sharing a household. Primary workflows: scanning grocery receipts on mobile, managing shopping lists collaboratively in-store, reviewing price trends at home. High mobile usage for scan and list flows; desktop for analytics and product management.

---

## 1. Information Architecture & Navigation

### Observed Pain Points
- [MEDIUM] Sidebar section labeled "Pages" is a generic catch-all grouping Analytics, Products, Rules, Receipts, and Import together. These serve very different purposes (daily use vs. configuration vs. analysis). The flat grouping does not signal frequency of use.
- [LOW] Sidebar uses Unicode characters as icons (approximate sign for chart-bar, gear for adjustments, trigram for receipt). These are visually inconsistent and semantically unclear -- a user scanning the sidebar cannot distinguish them at a glance. Source: `Sidebar.tsx` lines 38-46.
- [LOW] The "Conversions" page has no sidebar navigation entry at all. It is unreachable from the sidebar. Source: `Sidebar.tsx` lines 20-26 -- `pageLinks` array omits `/conversions`.
- [MEDIUM] No breadcrumbs on most pages. Only ProductDetailPage has breadcrumbs (`Products / {name}`). ReceiptReviewPage, StoreViewPage, ShoppingListPage, and all other detail views have no breadcrumb trail, making it unclear how to navigate back except via browser back or sidebar.
- [LOW] Dashboard has no page title in the heading -- it says "Welcome, {name}" which is friendly but does not orient the user to what page they are on. Every other page uses a descriptive heading.

### Gaps
- No way to navigate to ConversionsPage from anywhere in the app without knowing the URL.
- No "back" affordance on ReceiptReviewPage, AnalyticsPage, ImportPage, or ConversionsPage (ShoppingListPage and ProductDetailPage do have back links).

---

## 2. Content Clarity & Labeling

### Observed Pain Points
- [HIGH] "Matching Rules" is developer/system jargon. The RulesPage subtitle explains it ("Rules automatically match receipt line items to products"), but the sidebar label "Rules" alone is opaque. A non-technical user would not know what this does. Source: `Sidebar.tsx` line 23, `RulesPage.tsx` lines 133-138.
- [MEDIUM] Receipt review table header "Raw Name" exposes an internal data concept. Users see the receipt text already -- labeling it "Raw Name" adds no clarity and introduces system terminology. Source: `ReceiptReview.tsx` line 249.
- [MEDIUM] Receipt review table header "Match" for the product_id autocomplete column. "Match" is ambiguous -- match what to what? It is the product this line item maps to. Source: `ReceiptReview.tsx` line 253.
- [LOW] `conditionLabel` in RulesPage displays rule conditions like `exact 'CHKN'`, `contains 'BRST'`, `starts with 'BNL'`. The `matches` option shows as `matches 'pattern'` which actually means regex. "Regex matches" appears in the RuleFormModal dropdown but the table just says "matches" -- inconsistent labeling. Source: `RulesPage.tsx` lines 12-25, `RuleFormModal.tsx` lines 10-15.
- [LOW] The term "expensive" is used as a CSS semantic color name (e.g., `text-expensive`, `bg-expensive-subtle`). This leaks into the codebase but not into user-facing copy, so it is a code-level nomenclature concern only.
- [MEDIUM] "Condition Value" as a label in RuleFormModal. Users are entering text that appears on receipts (e.g., "BNLS CHKN BRST"). "Condition Value" is abstract. Source: `RuleFormModal.tsx` line 154.
- [LOW] ConversionsPage helper text says `1 from_unit = factor * to_unit` using snake_case variable names. Source: `ConversionsPage.tsx` line 128.

### Empty States
Empty states are handled consistently and well across pages. Every list, table, and section has a zero-data message with a call to action where appropriate:
- Dashboard: "Not enough purchase history yet. Keep scanning receipts!" / "No deals detected yet." / "No trips yet. Scan a receipt to get started!"
- ReceiptsPage: "No receipts yet." with "Scan Your First Receipt" button
- ListsIndexPage: "No shopping lists yet." with "Create your first list" button
- ProductsPage: "No products yet." with "Add Your First Product" button
- RulesPage: "No matching rules yet." with "Create Your First Rule" button
- ConversionsPage: descriptive explanation of built-in vs. custom conversions

### Monetary Formatting
- Currency formatting is handled by local `formatCurrency` functions defined separately in DashboardPage, ReceiptsPage, and StoreViewPage. Each returns `$${num.toFixed(2)}`. These are consistent but duplicated.
- Receipt review price column displays `$${price}` without toFixed(2), meaning raw decimal strings from the API are shown directly. If the API returns "3.5", it will show "$3.5" not "$3.50". Source: `ReceiptReview.tsx` line 295.
- Shopping list prices use `${item.estimated_price}` and `${item.cheapest_price}` directly without formatting to 2 decimal places. Source: `ShoppingListPage.tsx` lines 593, 577, 597.

---

## 3. Task Flow Completeness

### First-Time User Flow
- Setup -> Login works cleanly. SetupPage creates household + first user, redirects to dashboard.
- From dashboard, user sees empty states with prompts to scan receipts.
- Scan -> Upload -> Processing spinner -> WebSocket notification -> redirect to ReceiptReviewPage. This is well-connected.
- **Gap**: After reviewing a receipt and clicking "Confirm All", there is no navigation prompt. The user stays on the review page with a "Confirmed" badge. No link to "View all receipts" or "Scan another" or "Go to dashboard".

### Shopping List Flow
- Create list from ListsIndexPage or sidebar -> navigate to ShoppingListPage -> add items via autocomplete -> check items off -> share via modal.
- **Gap**: The "Share" modal generates a text representation of the list and offers copy/share. It does not generate a link for another user to join the household or view the list. The invite flow (JoinPage) is separate and unconnected from list sharing -- the share button shares list contents as text, not collaboration access. A user might expect "Share" to mean collaborative access.

### Invite Flow
- JoinPage validates invite token, shows household name and inviter name, allows account creation.
- **Gap**: There is no UI anywhere in the app to generate an invite link. No "Invite member" button exists in any page or sidebar. The invite token generation must happen outside the UI or is not yet implemented. Source: searched all pages and components, no reference to creating invites.

### Receipt Scanning Flow
- Camera capture uses `capture="environment"` on the file input, which correctly triggers rear camera on mobile.
- Multi-page support (up to 5 images) with clear count indicator.
- Processing state has a spinner with descriptive text.
- **Gap**: If the WebSocket connection fails or the `receipt.processed` event never arrives (timeout), the user is stuck on the processing spinner indefinitely. There is no timeout, fallback polling, or manual "check status" button. Source: `ReceiptScanner.tsx` lines 49-72 -- only listens for WS event, no timeout.

---

## 4. Mobile Usability

### Touch Targets
- [HIGH] Shopping list checkboxes are 24x24px (`w-6 h-6`). The minimum recommended touch target is 44x44px (Apple HIG) or 48x48dp (Material). These are significantly undersized for thumb interaction in a store. Source: `ShoppingListPage.tsx` line 543.
- [MEDIUM] Delete buttons on shopping list items (desktop) are 28px hit area (`p-1.5` on a 16px icon). On mobile these are hidden (`hidden sm:block`), which is good -- delete is swipe-only on mobile.
- [LOW] Priority up/down buttons on RulesPage are 14px icons (`w-3.5 h-3.5`) with no padding. Very small for touch. Source: `RulesPage.tsx` lines 193-214. However, this page is likely desktop-only usage.

### Swipe-to-Delete Discoverability
- [HIGH] Swipe-to-delete on shopping list items is the ONLY way to delete on mobile (the X button is `hidden sm:block`). There is no visual hint, no instructional tooltip, no long-press alternative. A user who has never encountered swipe-to-delete will not discover it. Source: `ShoppingListPage.tsx` lines 605-607 (delete button hidden on mobile), lines 480-515 (swipe handler).
- The swipe threshold is -80px, which is reasonable. The red background with trash icon is revealed during swipe, which provides good feedback once discovered.

### Sidebar Mobile Behavior
- Sidebar uses a slide-in overlay on mobile with a dark backdrop. The hamburger menu button is in the mobile header. This is a standard and clear pattern.
- The sidebar closes on any nav link click (`onClick={onClose}`), which is correct.

### Receipt Scanner on Mobile
- The large dashed-border capture area with camera icon and "Take Photo or Choose Image" text is clear and appropriately sized for mobile.
- `capture="environment"` attribute is present, which opens the camera directly on mobile.

---

## 5. Cognitive Load

### Dashboard
- Four summary cards + three content sections (Buy Again, Deals, Recent Trips). This is moderate density.
- [MEDIUM] "Likely Need Soon" section title differs from the internal concept name "Buy Again" used in the code and API (`getBuyAgain`). This is actually good for users -- but the urgency indicators use colored circle emojis (red/yellow/green/white) alongside text labels (Overdue/Running low/On the horizon/Well stocked). The emojis and labels are redundant but the emoji alone would be insufficient. Source: `DashboardPage.tsx` lines 9-21.

### Receipt Review
- [HIGH] The click-to-edit pattern on the EditableTable is not visually signaled. Cells look like plain text. There is no pencil icon, no hover underline, no "click to edit" tooltip. A user seeing the receipt review table for the first time has no way to know cells are editable until they accidentally click one. Source: `EditableCell.tsx` -- the non-editing state is a plain div with text, active state shows a ring but only after keyboard navigation.
- [MEDIUM] After matching a line item to a product, a modal immediately appears asking to create a matching rule. This interrupts the flow of matching multiple items sequentially. If you have 20 unmatched items, you get a modal after every match. Source: `ReceiptReview.tsx` lines 147-164.

### Product Detail Page
- Six sections stacked vertically: Price Trend, Photos, Aliases, Price Comparison, Transactions, Mealie Links. None are collapsible.
- [MEDIUM] For a product with extensive history, the Transactions section could be very long (unbounded list of all purchases). There is no pagination or "show more" pattern. Source: `ProductDetailPage.tsx` lines 493-535.
- The page is max-width 4xl, which is appropriate for the content density.

### Analytics Page
- Two sections: Trip Cost Chart and Product Price Trends table.
- [LOW] The Trip Cost Chart section has no axis labels or legend visible in the code -- it delegates to `TripCostChart` component which was not in the audit scope. The sparklines in the trends table are unlabeled (no y-axis, no hover values). Source: `AnalyticsPage.tsx` lines 90-95.

---

## 6. Consistency

### Action Positioning
- Primary actions (CTA buttons) are consistently in the top-right of page headers across ReceiptsPage, ListsIndexPage, ProductsPage, RulesPage, ConversionsPage. This is consistent.
- Modal footer buttons consistently place Cancel on the left and primary action on the right. Source: every Modal usage follows `<Cancel> <Primary>` order.

### Color Semantics
- Green (`success` variant) = good/cheap/matched/active. Used consistently.
- Red/orange (`expensive`/`warning`/`error` variants) = price increase/unmatched/delete/error. Mostly consistent, but:
  - [MEDIUM] Badge `warning` and `error` variants use identical styles (`bg-expensive-subtle text-expensive`). They are visually indistinguishable. Source: `Badge.tsx` lines 12-13. This means "needs review" badges look identical to "error" badges.
- Price increases show as `warning` variant (amber/red), price decreases as `success` (green). This is consistent across DashboardPage, ProductsPage, and AnalyticsPage.

### Delete Confirmation Pattern
- Destructive actions consistently use a modal with Cancel/Delete buttons.
- [MEDIUM] Delete button styling is inconsistent. ListsIndexPage uses `className="!bg-expensive hover:!bg-expensive/80"` with `!important` overrides. ProductDetailPage uses `className="bg-expensive text-white hover:opacity-90"`. ConversionsPage, RulesPage, and ProductMerge also use the inline `bg-expensive` override. This is a workaround because the Button component has no `danger` variant. Source: multiple files.

### Loading States
- Loading states consistently show "Loading {noun}..." text in `text-neutral-400`. This is consistent across all pages.
- [LOW] No skeleton/shimmer loading states anywhere. All loading states are text-only. For data-heavy pages like ProductsPage or AnalyticsPage, content layout shift occurs when data loads.

---

## 7. Accessibility

### ARIA Labels
- Interactive SVG buttons consistently have `aria-label` attributes: image remove buttons, list item checkboxes, delete buttons, priority buttons, menu toggle. This is well-implemented.
- Modal has `role="dialog"`, `aria-modal="true"`, and `aria-label={title}`. Source: `Modal.tsx` lines 46-48.
- Error messages use `role="alert"` on SetupPage, LoginPage, JoinPage.

### Keyboard Navigation
- EditableTable has comprehensive keyboard navigation via `useTableNavigation` hook: arrow keys, Enter to edit, Escape to cancel, Tab to move between cells.
- [MEDIUM] Shopping list items have no keyboard-accessible delete mechanism. The delete button is `hidden sm:block` (hidden on mobile), and swipe is touch-only. On desktop, the delete button exists but the list items themselves have no keyboard shortcut for deletion. Source: `ShoppingListPage.tsx` lines 604-614.
- [LOW] List name editing on ShoppingListPage relies on click-to-edit with no keyboard affordance to enter edit mode (though once editing, Enter/Escape work). Source: `ShoppingListPage.tsx` lines 315-319.

### Color Contrast
- [MEDIUM] Secondary text uses `text-neutral-400` which maps to the Silver Blue color (`#9497a9`). On white background, this is approximately 3.3:1 contrast ratio, which fails WCAG AA for normal text (requires 4.5:1). This is used pervasively for helper text, timestamps, and secondary labels across every page.
- [MEDIUM] The `text-neutral-500` used in sidebar nav links maps to approximately `#686b82` (Cool Gray). On white this is roughly 5.1:1, which passes AA for normal text but is borderline.

### Emoji as Status Indicators
- [MEDIUM] Dashboard "Buy Again" urgency uses colored circle emojis as the primary visual indicator. Text labels ("Overdue", "Running low", etc.) appear alongside but in a smaller, lighter style. Screen readers would announce the emoji Unicode characters, not the intended meaning. The `urgencyEmoji` function returns raw emoji without wrapping in an element with `aria-label`. Source: `DashboardPage.tsx` lines 9-14, 34.

---

## Content Findings Summary

- **Labeling**: "Rules" / "Matching Rules" is jargon. "Raw Name" and "Match" are internal terms leaking into UI. "Condition Value" is abstract. `from_unit` snake_case appears in helper text.
- **Nomenclature**: `expensive` as a color name in Tailwind config is an internal naming choice that does not leak to users. Column names like `raw_name`, `product_id` appear as table header text ("Raw Name") and conceptual labels ("Match").
- **Grouping/IA**: Sidebar "Pages" section is a flat dump of unrelated features. Conversions page is unreachable. No separation between daily-use features and configuration/setup features.
- **Progressive disclosure**: Product detail shows all sections always, no collapsing. Receipt review shows rule creation modal after every match (no batching/deferral). Transaction history is unbounded.
- **Hierarchy**: Label/hint/placeholder pattern is well-implemented on Input component (label above, placeholder inside, helperText below, error replaces helper). This is consistent wherever Input is used. Raw `<input>` elements (sidebar list creation, product search, shopping list add) lack this structure.

---

## Patterns Found in Similar Products

- **Actual Budget** (the stated inspiration): Uses a similar sidebar with accounts listed, rules page for payee matching. Actual calls them "Rules" too, but Actual's user base is finance-savvy. For a grocery app used by non-technical users, the term may not transfer well.
- **Grocy** (grocery/household management): Uses "Stock overview" rather than "Products", "Purchase" rather than "Scan Receipt". Terminology is more action-oriented.
- **AnyList / OurGroceries** (shopping list apps): Swipe-to-delete is standard in these apps BUT they also offer long-press context menus as an alternative. Neither relies solely on swipe.
- **Apple Reminders / Google Keep**: Checkboxes for list items use the full row as a tap target, not just a small checkbox.

---

## Confidence Notes

- Color contrast ratios are estimated from hex values in DESIGN.md; actual rendered contrast depends on the Tailwind config mapping which was not inspected. The `text-neutral-400` -> `#9497a9` mapping is assumed from the design system spec.
- The TripCostChart and Sparkline components were not fully audited (only their usage was observed).
- WebSocket reliability concern (stuck processing spinner) is based on code review only -- actual failure behavior depends on backend reconnection logic not visible in frontend code.
- The absence of an "invite member" UI was confirmed by searching all audited files; it is possible this exists in a component not in the audit scope.

---

## Sources

- `/Users/mstefanko/cartledger/web/src/components/ui/Sidebar.tsx:20-26` -- Conversions missing from nav, "Pages" flat grouping
- `/Users/mstefanko/cartledger/web/src/components/ui/Sidebar.tsx:38-46` -- Unicode character icons
- `/Users/mstefanko/cartledger/web/src/components/ui/Badge.tsx:12-13` -- warning and error variants identical
- `/Users/mstefanko/cartledger/web/src/components/ui/Button.tsx` -- no danger variant exists
- `/Users/mstefanko/cartledger/web/src/components/ui/EditableTable/EditableCell.tsx:108-122` -- no visual edit affordance
- `/Users/mstefanko/cartledger/web/src/components/ui/EditableTable/AutocompleteCell.tsx:264-282` -- unmatched italic state only hint
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx:147-164` -- rule modal after every match
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx:249,253` -- "Raw Name" and "Match" headers
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptReview.tsx:295` -- price without toFixed(2)
- `/Users/mstefanko/cartledger/web/src/components/receipts/ReceiptScanner.tsx:49-72` -- no WS timeout
- `/Users/mstefanko/cartledger/web/src/pages/DashboardPage.tsx:9-21` -- emoji urgency indicators
- `/Users/mstefanko/cartledger/web/src/pages/ShoppingListPage.tsx:543` -- 24x24 checkbox
- `/Users/mstefanko/cartledger/web/src/pages/ShoppingListPage.tsx:480-515,605-607` -- swipe-only delete on mobile
- `/Users/mstefanko/cartledger/web/src/pages/ShoppingListPage.tsx:577,593,597` -- unformatted prices
- `/Users/mstefanko/cartledger/web/src/pages/RulesPage.tsx:12-25,133-138` -- jargon labels
- `/Users/mstefanko/cartledger/web/src/pages/ProductDetailPage.tsx:493-535` -- unbounded transaction list
- `/Users/mstefanko/cartledger/web/src/pages/ConversionsPage.tsx:128` -- snake_case in helper text
- `/Users/mstefanko/cartledger/web/src/components/matching/RuleFormModal.tsx:10-15,154` -- inconsistent condition terminology
- `/Users/mstefanko/cartledger/DESIGN.md:26-27` -- neutral color values for contrast estimation

## Status: COMPLETE
