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
  type USDAFDCConfigBody,
  type TestResult,
} from '@/api/integrations'
import {
  getProductEnrichmentSettings,
  updateProductEnrichmentSettings,
  type ProductEnrichmentSettingsUpdate,
} from '@/api/product-enrichment'
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

      <ProductEnrichmentSettingsCard />

      {isLoading ? (
        <p className="text-body text-neutral-400">Loading integrations...</p>
      ) : (
        <>
          {REAL_INTEGRATIONS.map(({ type, name, description }) => (
            <MealieCard
              key={type}
              type={type}
              name={name}
              description={description}
              integration={integrationsByType.get(type) ?? null}
            />
          ))}
          <USDACard integration={integrationsByType.get('usda_fdc') ?? null} />
        </>
      )}

      {COMING_SOON.map((entry) => (
        <ComingSoonCard key={entry.name} name={entry.name} description={entry.description} />
      ))}
    </div>
  )
}

// ---- Product enrichment settings ----

function ProductEnrichmentSettingsCard() {
  const queryClient = useQueryClient()
  const { data: settings, isLoading } = useQuery({
    queryKey: ['product-enrichment-settings'],
    queryFn: getProductEnrichmentSettings,
  })
  const mutation = useMutation({
    mutationFn: (data: ProductEnrichmentSettingsUpdate) => updateProductEnrichmentSettings(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['product-enrichment-settings'] })
    },
  })

  const update = (data: ProductEnrichmentSettingsUpdate) => {
    mutation.mutate(data)
  }
  const usdaAvailability = settings?.provider_availability.usda_fdc

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h3 className="font-display text-feature font-semibold text-neutral-900">Food product enrichment</h3>
          <p className="text-body text-neutral-500 mt-1">Barcode lookups, nutrition sources, and automatic lookup consent.</p>
        </div>
        {settings && !settings.global_enabled && (
          <span className="text-small font-medium text-red-700 bg-red-50 px-2 py-0.5 rounded-full">
            Disabled by operator
          </span>
        )}
      </div>

      {isLoading || !settings ? (
        <p className="mt-4 text-body text-neutral-400">Loading enrichment settings...</p>
      ) : (
        <div className="mt-5 grid gap-3">
          <SettingToggle
            label="Manual lookup"
            description="Allow explicit product lookup buttons."
            checked={settings.manual_lookup_enabled}
            disabled={mutation.isPending || !settings.global_enabled}
            onChange={(checked) => update({ manual_lookup_enabled: checked })}
          />
          <SettingToggle
            label="Open Food Facts"
            description="Use barcode metadata from Open Food Facts."
            checked={settings.provider_openfoodfacts_enabled}
            disabled={mutation.isPending || !settings.global_enabled}
            onChange={(checked) => update({ provider_openfoodfacts_enabled: checked })}
          />
          <SettingToggle
            label="USDA FoodData Central"
            description={
              usdaAvailability?.env_fallback_configured
                ? `Nutrition fallback available via ${usdaAvailability.credential_source === 'env' ? 'operator key' : 'household key'}.`
                : 'Requires a household API key or operator fallback key.'
            }
            checked={settings.provider_usda_fdc_enabled}
            disabled={mutation.isPending || !settings.global_enabled || !usdaAvailability?.configured}
            onChange={(checked) => update({ provider_usda_fdc_enabled: checked })}
          />
          <SettingToggle
            label="Automatic lookup on scan"
            description="Queue external lookups after receipt scans."
            checked={settings.auto_on_scan_enabled}
            disabled={mutation.isPending || !settings.global_enabled}
            onChange={(checked) => update({ auto_on_scan_enabled: checked })}
          />
          <SettingToggle
            label="Scheduled refresh"
            description="Allow future background refresh sweeps."
            checked={settings.scheduled_sweep_enabled}
            disabled={mutation.isPending || !settings.global_enabled}
            onChange={(checked) => update({ scheduled_sweep_enabled: checked })}
          />
        </div>
      )}
    </div>
  )
}

function SettingToggle({
  label,
  description,
  checked,
  disabled,
  onChange,
}: {
  label: string
  description: string
  checked: boolean
  disabled?: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="flex items-center justify-between gap-4 rounded-xl border border-neutral-100 px-3 py-2">
      <span>
        <span className="block text-caption font-medium text-neutral-900">{label}</span>
        <span className="block text-small text-neutral-400">{description}</span>
      </span>
      <input
        type="checkbox"
        className="h-5 w-5 rounded border-neutral-300 text-brand focus:ring-brand"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
    </label>
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

function USDACard({ integration }: { integration: Integration | null }) {
  const queryClient = useQueryClient()
  const configured = !!integration?.configured
  const enabled = integration?.enabled ?? false
  const [expanded, setExpanded] = useState(false)
  const [apiKey, setAPIKey] = useState('')
  const [testResult, setTestResult] = useState<TestResult | null>(null)
  const [saveMessage, setSaveMessage] = useState<{ kind: 'success' | 'error'; text: string } | null>(null)
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false)

  const saveMutation = useMutation({
    mutationFn: (body: USDAFDCConfigBody) => updateIntegration('usda_fdc', body),
    onSuccess: () => {
      setSaveMessage({ kind: 'success', text: 'Saved.' })
      setAPIKey('')
      void queryClient.invalidateQueries({ queryKey: ['integrations'] })
      void queryClient.invalidateQueries({ queryKey: ['product-enrichment-settings'] })
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Failed to save.'
      setSaveMessage({ kind: 'error', text: msg })
    },
  })

  const testMutation = useMutation({
    mutationFn: (body: USDAFDCConfigBody) => testIntegration('usda_fdc', body),
    onSuccess: (result) => {
      setTestResult(result)
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Test failed.'
      setTestResult({ ok: false, message: msg })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteIntegration('usda_fdc'),
    onSuccess: () => {
      setConfirmDeleteOpen(false)
      setAPIKey('')
      setTestResult(null)
      setSaveMessage({ kind: 'success', text: 'Integration removed.' })
      setExpanded(false)
      void queryClient.invalidateQueries({ queryKey: ['integrations'] })
      void queryClient.invalidateQueries({ queryKey: ['product-enrichment-settings'] })
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Failed to delete.'
      setSaveMessage({ kind: 'error', text: msg })
    },
  })

  const canSave = apiKey.trim() !== ''

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-3 flex-wrap">
            <h3 className="font-display text-feature font-semibold text-neutral-900">USDA FoodData Central</h3>
            <StatusBadge configured={configured} enabled={enabled} />
          </div>
          <p className="text-body text-neutral-500 mt-1">Authoritative branded-food nutrition lookup by API key.</p>
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
            label="API Key"
            type="password"
            placeholder={configured ? 'Enter key to update' : 'paste-api-key'}
            value={apiKey}
            onChange={(e) => setAPIKey(e.target.value)}
            helperText={
              configured
                ? 'For security, the existing key is not shown. Saving requires re-entering the key.'
                : 'Create a free key from USDA FoodData Central.'
            }
          />

          {testResult && (
            <p className={testResult.ok ? 'text-small text-green-600' : 'text-small text-expensive'}>
              {testResult.ok
                ? `Connection OK${testResult.message ? ` — ${testResult.message}` : ''}`
                : `Connection failed${testResult.message ? `: ${testResult.message}` : ''}`}
            </p>
          )}

          {saveMessage && (
            <p className={saveMessage.kind === 'success' ? 'text-small text-green-600' : 'text-small text-expensive'}>
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
              onClick={() => {
                setTestResult(null)
                testMutation.mutate({ api_key: apiKey.trim() })
              }}
              disabled={!canSave || testMutation.isPending}
            >
              {testMutation.isPending ? 'Testing...' : 'Test connection'}
            </Button>
            <Button
              size="sm"
              onClick={() => saveMutation.mutate({ api_key: apiKey.trim() })}
              disabled={!canSave || saveMutation.isPending}
            >
              {saveMutation.isPending ? 'Saving...' : 'Save'}
            </Button>
          </div>
        </div>
      )}

      <Modal
        open={confirmDeleteOpen}
        onClose={() => setConfirmDeleteOpen(false)}
        title="Remove USDA integration?"
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
          The stored USDA API key will be deleted. Existing accepted nutrition remains in CartLedger.
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
