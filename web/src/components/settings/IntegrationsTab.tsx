import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  listIntegrations,
  updateIntegration,
  deleteIntegration,
  testIntegration,
  type Integration,
  type IntegrationType,
  type MealieConfigBody,
  type TestResult,
} from '@/api/integrations'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import { ApiClientError } from '@/api/client'

// Real integrations backed by the API. Add entries here as the backend grows.
const REAL_INTEGRATIONS: Array<{
  type: IntegrationType
  name: string
  description: string
}> = [
  {
    type: 'mealie',
    name: 'Mealie',
    description: 'Import recipes and shopping lists from a self-hosted Mealie instance.',
  },
]

// Frontend-only placeholder list so the tab shows the design scaling to more
// integrations. No backend ping — purely visual.
const COMING_SOON: Array<{ name: string; description: string }> = [
  {
    name: 'Tandoor',
    description: 'Self-hosted recipe manager with a Mealie-shaped API.',
  },
  {
    name: 'Grocy',
    description: 'Self-hosted groceries & household management — stock, prices, barcodes.',
  },
  {
    name: 'OpenFoodFacts',
    description: 'Free barcode lookup for product and nutrition info.',
  },
  {
    name: 'USDA FoodData Central',
    description: 'Authoritative nutrition data by free API key.',
  },
  {
    name: 'Todoist',
    description: 'Sync shopping lists with a Todoist project.',
  },
]

function IntegrationsTab() {
  const { data, isLoading } = useQuery({
    queryKey: ['integrations'],
    queryFn: listIntegrations,
  })

  const integrationsByType = new Map<IntegrationType, Integration>()
  for (const i of data ?? []) integrationsByType.set(i.type, i)

  return (
    <div className="flex flex-col gap-4 max-w-2xl">
      <p className="text-body text-neutral-500">
        Connect CartLedger to external services. Credentials are stored per
        household; disable or delete a connection any time.
      </p>

      {isLoading ? (
        <p className="text-body text-neutral-400">Loading integrations...</p>
      ) : (
        REAL_INTEGRATIONS.map(({ type, name, description }) => (
          <MealieCard
            key={type}
            type={type}
            name={name}
            description={description}
            integration={integrationsByType.get(type) ?? null}
          />
        ))
      )}

      {COMING_SOON.map((entry) => (
        <ComingSoonCard key={entry.name} name={entry.name} description={entry.description} />
      ))}
    </div>
  )
}

// ---- Mealie (and future {base_url, token}) integration card ----

interface MealieCardProps {
  type: IntegrationType
  name: string
  description: string
  integration: Integration | null
}

function MealieCard({ type, name, description, integration }: MealieCardProps) {
  const queryClient = useQueryClient()
  const configured = !!integration?.configured
  const enabled = integration?.enabled ?? false

  const [expanded, setExpanded] = useState(false)
  const [baseUrl, setBaseUrl] = useState(integration?.base_url ?? '')
  const [token, setToken] = useState('')
  const [testResult, setTestResult] = useState<TestResult | null>(null)
  const [saveMessage, setSaveMessage] = useState<{ kind: 'success' | 'error'; text: string } | null>(null)
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false)

  useEffect(() => {
    if (!expanded && integration?.base_url && baseUrl === '') {
      setBaseUrl(integration.base_url)
    }
  }, [integration?.base_url, expanded, baseUrl])

  const saveMutation = useMutation({
    mutationFn: (body: MealieConfigBody) => updateIntegration(type, body),
    onSuccess: () => {
      setSaveMessage({ kind: 'success', text: 'Saved.' })
      setToken('')
      void queryClient.invalidateQueries({ queryKey: ['integrations'] })
      void queryClient.invalidateQueries({ queryKey: ['mealie-status'] })
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Failed to save.'
      setSaveMessage({ kind: 'error', text: msg })
    },
  })

  const testMutation = useMutation({
    mutationFn: (body: MealieConfigBody) => testIntegration(type, body),
    onSuccess: (result) => {
      setTestResult(result)
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Test failed.'
      setTestResult({ ok: false, message: msg })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteIntegration(type),
    onSuccess: () => {
      setConfirmDeleteOpen(false)
      setBaseUrl('')
      setToken('')
      setTestResult(null)
      setSaveMessage({ kind: 'success', text: 'Integration removed.' })
      setExpanded(false)
      void queryClient.invalidateQueries({ queryKey: ['integrations'] })
      void queryClient.invalidateQueries({ queryKey: ['mealie-status'] })
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Failed to delete.'
      setSaveMessage({ kind: 'error', text: msg })
    },
  })

  // Save is disabled when token is blank AND we have no existing configured row.
  // Backend rejects blank tokens with 400, so we can't send a "no-op save"
  // against an existing row either — the user must re-enter the token to
  // change the URL. Surface that via helperText.
  const canSave = baseUrl.trim() !== '' && token.trim() !== ''

  function handleSave() {
    setSaveMessage(null)
    saveMutation.mutate({ base_url: baseUrl.trim(), token: token.trim() })
  }

  function handleTest() {
    setTestResult(null)
    testMutation.mutate({ base_url: baseUrl.trim(), token: token.trim() })
  }

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-3 flex-wrap">
            <h3 className="font-display text-feature font-semibold text-neutral-900">{name}</h3>
            <StatusBadge configured={configured} enabled={enabled} />
          </div>
          <p className="text-body text-neutral-500 mt-1">{description}</p>
          {configured && integration?.base_url && !expanded && (
            <p className="text-small text-neutral-400 mt-1 truncate">{integration.base_url}</p>
          )}
        </div>
        <Button
          size="sm"
          variant={expanded ? 'secondary' : 'subtle'}
          onClick={() => {
            setExpanded((v) => !v)
            setSaveMessage(null)
            setTestResult(null)
          }}
        >
          {expanded ? 'Close' : configured ? 'Edit' : 'Connect'}
        </Button>
      </div>

      {expanded && (
        <div className="flex flex-col gap-4 mt-5 pt-5 border-t border-neutral-100">
          <Input
            label="Base URL"
            placeholder="https://mealie.example.com"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
          />
          <Input
            label="API Token"
            type="password"
            placeholder={configured ? 'Enter token to update' : 'paste-token-here'}
            value={token}
            onChange={(e) => setToken(e.target.value)}
            helperText={
              configured
                ? 'For security, the existing token is not shown. Leave blank only to cancel — saving requires re-entering the token.'
                : 'Create a long-lived API token in Mealie under User → API Tokens.'
            }
          />

          {testResult && (
            <p
              className={
                testResult.ok
                  ? 'text-small text-green-600'
                  : 'text-small text-expensive'
              }
            >
              {testResult.ok
                ? `Connection OK${testResult.message ? ` — ${testResult.message}` : ''}`
                : `Connection failed${testResult.message ? `: ${testResult.message}` : ''}`}
            </p>
          )}

          {saveMessage && (
            <p
              className={
                saveMessage.kind === 'success'
                  ? 'text-small text-green-600'
                  : 'text-small text-expensive'
              }
            >
              {saveMessage.text}
            </p>
          )}

          <div className="flex flex-wrap gap-2 justify-end">
            {configured && (
              <Button
                size="sm"
                variant="secondary"
                className="text-red-600 hover:text-red-700"
                onClick={() => setConfirmDeleteOpen(true)}
                disabled={deleteMutation.isPending}
              >
                Delete
              </Button>
            )}
            <Button
              size="sm"
              variant="outlined"
              onClick={handleTest}
              disabled={!canSave || testMutation.isPending}
            >
              {testMutation.isPending ? 'Testing...' : 'Test connection'}
            </Button>
            <Button size="sm" onClick={handleSave} disabled={!canSave || saveMutation.isPending}>
              {saveMutation.isPending ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </div>
      )}

      <Modal
        open={confirmDeleteOpen}
        onClose={() => setConfirmDeleteOpen(false)}
        title={`Remove ${name} integration?`}
        footer={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setConfirmDeleteOpen(false)}
              disabled={deleteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              className="bg-red-600 text-white hover:bg-red-700"
              size="sm"
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Removing...' : 'Remove'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          The stored credentials will be deleted. Imported recipes and lists already in CartLedger are kept.
        </p>
      </Modal>
    </div>
  )
}

// ---- Placeholder card for not-yet-built integrations ----

function ComingSoonCard({ name, description }: { name: string; description: string }) {
  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5 opacity-75">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-3 flex-wrap">
            <h3 className="font-display text-feature font-semibold text-neutral-900">{name}</h3>
            <span className="text-small font-medium text-neutral-500 bg-neutral-50 px-2 py-0.5 rounded-full">
              Coming soon
            </span>
          </div>
          <p className="text-body text-neutral-500 mt-1">{description}</p>
        </div>
      </div>
    </div>
  )
}

// ---- Status badge ----

function StatusBadge({ configured, enabled }: { configured: boolean; enabled: boolean }) {
  if (!configured) {
    return (
      <span className="text-small font-medium text-neutral-500 bg-neutral-50 px-2 py-0.5 rounded-full">
        Not configured
      </span>
    )
  }
  if (!enabled) {
    return (
      <span className="text-small font-medium text-neutral-600 bg-neutral-100 px-2 py-0.5 rounded-full">
        Disabled
      </span>
    )
  }
  return (
    <span className="text-small font-medium text-green-700 bg-green-50 px-2 py-0.5 rounded-full">
      Configured
    </span>
  )
}

export default IntegrationsTab
