import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery, useMutation } from '@tanstack/react-query'
import {
  getMealieStatus,
  listMealieRecipes,
  listMealieLists,
  importMealieRecipe,
  importMealieList,
} from '@/api/import'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
import type { ImportedRecipe, ImportedShoppingList } from '@/types'
import SpreadsheetImportTab from './import/spreadsheet/SpreadsheetImportTab'

type TopTab = 'spreadsheet' | 'mealie'

const TOP_TAB_LABELS: Record<TopTab, string> = {
  spreadsheet: 'Spreadsheet',
  mealie: 'Mealie',
}

function ImportPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const raw = searchParams.get('tab')
  const activeTopTab: TopTab = raw === 'mealie' ? 'mealie' : 'spreadsheet'

  function setTopTab(t: TopTab) {
    setSearchParams({ tab: t })
  }

  return (
    <div className="py-8">
      <div className="max-w-5xl">
        <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-2">
          Import
        </h1>
        <p className="text-body text-neutral-500 mb-6">
          Bring receipts and recipes into CartLedger.
        </p>

        {/* Top tab bar */}
        <div className="flex gap-1 border-b border-neutral-200 mb-6">
          {(Object.keys(TOP_TAB_LABELS) as TopTab[]).map((tab) => (
            <button
              key={tab}
              type="button"
              onClick={() => setTopTab(tab)}
              className={[
                'px-4 py-2.5 text-caption font-medium whitespace-nowrap transition-colors -mb-px border-b-2 cursor-pointer',
                activeTopTab === tab
                  ? 'border-brand text-brand'
                  : 'border-transparent text-neutral-500 hover:text-neutral-900',
              ].join(' ')}
            >
              {TOP_TAB_LABELS[tab]}
            </button>
          ))}
        </div>

        {activeTopTab === 'spreadsheet' && <SpreadsheetImportTab />}
        {activeTopTab === 'mealie' && <MealieTab />}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Mealie tab (pre-existing content, relocated — unchanged behavior).
// ---------------------------------------------------------------------------

type MealieSubTab = 'recipes' | 'lists'

function MealieTab() {
  const [activeTab, setActiveTab] = useState<MealieSubTab>('recipes')
  const [importResult, setImportResult] = useState<ImportedRecipe | null>(null)
  const [listResult, setListResult] = useState<ImportedShoppingList | null>(null)

  const statusQuery = useQuery({
    queryKey: ['mealie-status'],
    queryFn: getMealieStatus,
  })

  const status = statusQuery.data
  const isConnected = status?.configured && status?.connected

  return (
    <div className="max-w-4xl">
      <ConnectionStatus status={status} isLoading={statusQuery.isLoading} />

      {!isConnected && !statusQuery.isLoading && (
        <div className="mt-6 bg-white rounded-2xl shadow-subtle p-6">
          <h2 className="font-display text-feature font-semibold text-neutral-900 mb-2">Not Configured</h2>
          <p className="text-body text-neutral-500 mb-4">
            Mealie integration isn&apos;t connected for this household yet.
          </p>
          <Link
            to="/settings?tab=integrations"
            className="inline-flex items-center gap-2 text-caption font-medium text-brand hover:text-brand-dark"
          >
            Manage in Settings → Integrations
          </Link>
        </div>
      )}

      {isConnected && (
        <>
          <div className="flex gap-1 mt-6 mb-6 bg-neutral-50 rounded-xl p-1 w-fit">
            <button
              type="button"
              onClick={() => setActiveTab('recipes')}
              className={[
                'px-4 py-2 rounded-lg text-caption font-medium transition-colors cursor-pointer',
                activeTab === 'recipes'
                  ? 'bg-white text-neutral-900 shadow-micro'
                  : 'text-neutral-500 hover:text-neutral-700',
              ].join(' ')}
            >
              Recipes
            </button>
            <button
              type="button"
              onClick={() => setActiveTab('lists')}
              className={[
                'px-4 py-2 rounded-lg text-caption font-medium transition-colors cursor-pointer',
                activeTab === 'lists'
                  ? 'bg-white text-neutral-900 shadow-micro'
                  : 'text-neutral-500 hover:text-neutral-700',
              ].join(' ')}
            >
              Shopping Lists
            </button>
          </div>

          {activeTab === 'recipes' && <RecipesTab onImported={setImportResult} />}
          {activeTab === 'lists' && <ListsTab onImported={setListResult} />}
        </>
      )}

      <Modal
        open={!!importResult}
        onClose={() => setImportResult(null)}
        title={importResult ? `Imported: ${importResult.recipe_name}` : ''}
        footer={
          <Button size="sm" onClick={() => setImportResult(null)}>
            Done
          </Button>
        }
      >
        {importResult && <RecipeImportResult result={importResult} />}
      </Modal>

      <Modal
        open={!!listResult}
        onClose={() => setListResult(null)}
        title="Shopping List Imported"
        footer={
          <Button size="sm" onClick={() => setListResult(null)}>
            Done
          </Button>
        }
      >
        {listResult && (
          <div className="space-y-3">
            <p className="text-body text-neutral-600">
              Created list <span className="font-medium text-neutral-900">&quot;{listResult.list_name}&quot;</span>
            </p>
            <div className="flex gap-4 text-caption text-neutral-500">
              <span>{listResult.items_imported} items imported</span>
              <span>{listResult.items_matched} matched to products</span>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

// --- Connection Status ---

function ConnectionStatus({
  status,
  isLoading,
}: {
  status: { configured: boolean; connected: boolean; mealie_url: string | null; error: string | null } | undefined
  isLoading: boolean
}) {
  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-caption text-neutral-400">
        <div className="w-3 h-3 rounded-full bg-neutral-200 animate-pulse" />
        Checking connection...
      </div>
    )
  }

  if (!status) {
    return (
      <div className="flex items-center gap-2 text-caption text-neutral-400">
        <div className="w-3 h-3 rounded-full bg-neutral-300" />
        Unable to check status
      </div>
    )
  }

  if (status.configured && status.connected) {
    return (
      <div className="flex items-center gap-3">
        <div className="flex items-center gap-2 text-caption text-success-dark">
          <div className="w-3 h-3 rounded-full bg-success" />
          Connected
        </div>
        {status.mealie_url && (
          <span className="text-small text-neutral-400">{status.mealie_url}</span>
        )}
      </div>
    )
  }

  return (
    <div className="flex items-center gap-2 text-caption text-expensive">
      <div className="w-3 h-3 rounded-full bg-expensive" />
      {status.error ?? 'Not configured'}
    </div>
  )
}

// --- Recipes Tab ---

function RecipesTab({ onImported }: { onImported: (result: ImportedRecipe) => void }) {
  const recipesQuery = useQuery({
    queryKey: ['mealie-recipes'],
    queryFn: listMealieRecipes,
  })

  const importMutation = useMutation({
    mutationFn: importMealieRecipe,
    onSuccess: (result) => {
      onImported(result)
    },
  })

  if (recipesQuery.isLoading) {
    return <p className="text-body text-neutral-400">Loading recipes...</p>
  }

  if (recipesQuery.isError) {
    return <p className="text-body text-expensive">Failed to load recipes from Mealie.</p>
  }

  const recipes = recipesQuery.data ?? []

  if (recipes.length === 0) {
    return (
      <div className="bg-white rounded-2xl shadow-subtle p-6">
        <p className="text-body text-neutral-400">No recipes found in your Mealie instance.</p>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {recipes.map((recipe) => (
        <div
          key={recipe.id}
          className="bg-white rounded-2xl shadow-subtle px-5 py-4 flex items-center justify-between gap-4"
        >
          <div className="min-w-0 flex-1">
            <p className="text-body-medium font-medium text-neutral-900 truncate">
              {recipe.name}
            </p>
            {recipe.description && (
              <p className="text-small text-neutral-400 truncate mt-0.5">
                {recipe.description}
              </p>
            )}
            <div className="flex items-center gap-3 mt-1">
              {recipe.total_time && (
                <span className="text-small text-neutral-400">{recipe.total_time}</span>
              )}
              {recipe.servings && (
                <span className="text-small text-neutral-400">{recipe.servings} servings</span>
              )}
            </div>
          </div>
          <Button
            size="sm"
            variant="subtle"
            onClick={() => importMutation.mutate(recipe.slug)}
            disabled={importMutation.isPending && importMutation.variables === recipe.slug}
          >
            {importMutation.isPending && importMutation.variables === recipe.slug
              ? 'Importing...'
              : 'Import'}
          </Button>
        </div>
      ))}
    </div>
  )
}

// --- Lists Tab ---

function ListsTab({ onImported }: { onImported: (result: ImportedShoppingList) => void }) {
  const listsQuery = useQuery({
    queryKey: ['mealie-lists'],
    queryFn: listMealieLists,
  })

  const importMutation = useMutation({
    mutationFn: importMealieList,
    onSuccess: (result) => {
      onImported(result)
    },
  })

  if (listsQuery.isLoading) {
    return <p className="text-body text-neutral-400">Loading shopping lists...</p>
  }

  if (listsQuery.isError) {
    return <p className="text-body text-expensive">Failed to load shopping lists from Mealie.</p>
  }

  const lists = listsQuery.data ?? []

  if (lists.length === 0) {
    return (
      <div className="bg-white rounded-2xl shadow-subtle p-6">
        <p className="text-body text-neutral-400">No shopping lists found in your Mealie instance.</p>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      {lists.map((list) => (
        <div
          key={list.id}
          className="bg-white rounded-2xl shadow-subtle px-5 py-4 flex items-center justify-between gap-4"
        >
          <div className="min-w-0 flex-1">
            <p className="text-body-medium font-medium text-neutral-900 truncate">
              {list.name}
            </p>
            <p className="text-small text-neutral-400 mt-0.5">
              {list.item_count} items
            </p>
          </div>
          <Button
            size="sm"
            variant="subtle"
            onClick={() => importMutation.mutate(list.id)}
            disabled={importMutation.isPending && importMutation.variables === list.id}
          >
            {importMutation.isPending && importMutation.variables === list.id
              ? 'Importing...'
              : 'Import'}
          </Button>
        </div>
      ))}
    </div>
  )
}

// --- Recipe Import Result ---

function RecipeImportResult({ result }: { result: ImportedRecipe }) {
  const matched = result.items.filter((i) => i.matched)
  const unmatched = result.items.filter((i) => !i.matched)

  return (
    <div className="space-y-4">
      {result.total_cost && (
        <div className="bg-brand-subtle rounded-xl p-4 flex items-center justify-between">
          <span className="text-body-medium font-medium text-neutral-900">Total Recipe Cost</span>
          <span className="font-display text-feature font-semibold text-brand">
            ${parseFloat(result.total_cost).toFixed(2)}
          </span>
        </div>
      )}

      {matched.length > 0 && (
        <div>
          <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide mb-2">
            Matched ({matched.length})
          </p>
          <div className="space-y-1">
            {matched.map((item, i) => (
              <div key={i} className="flex items-center justify-between py-2 px-3 bg-neutral-50 rounded-xl">
                <div className="min-w-0 flex-1">
                  <span className="text-caption text-neutral-900 font-medium">
                    {item.product_name ?? item.mealie_food_name}
                  </span>
                  {item.quantity && (
                    <span className="text-small text-neutral-400 ml-2">
                      {item.quantity} {item.unit ?? ''}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  {item.cost && (
                    <span className="text-caption font-medium text-neutral-900">
                      ${parseFloat(item.cost).toFixed(2)}
                    </span>
                  )}
                  <Badge variant="success">Matched</Badge>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {unmatched.length > 0 && (
        <div>
          <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide mb-2">
            Unmatched ({unmatched.length})
          </p>
          <div className="space-y-1">
            {unmatched.map((item, i) => (
              <div key={i} className="flex items-center justify-between py-2 px-3 bg-expensive/5 rounded-xl border border-expensive/20">
                <div className="min-w-0 flex-1">
                  <span className="text-caption text-neutral-900 font-medium">
                    {item.mealie_food_name}
                  </span>
                  {item.quantity && (
                    <span className="text-small text-neutral-400 ml-2">
                      {item.quantity} {item.unit ?? ''}
                    </span>
                  )}
                </div>
                <Badge variant="error">Unmatched</Badge>
              </div>
            ))}
          </div>
          <p className="text-small text-neutral-400 mt-2">
            Unmatched ingredients were created as new products. Match them to existing products for accurate costing.
          </p>
        </div>
      )}
    </div>
  )
}

export default ImportPage
