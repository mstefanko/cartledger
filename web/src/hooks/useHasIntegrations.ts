import { useQuery } from '@tanstack/react-query'
import { listIntegrations } from '@/api/integrations'

/**
 * Shared hook for gating UI on at least one configured+enabled integration.
 * The query key matches `IntegrationsTab` so both read from the same cache —
 * saving/deleting in the tab immediately refreshes the sidebar/route guard.
 */
export function useHasIntegrations() {
  const { data, isLoading } = useQuery({
    queryKey: ['integrations'],
    queryFn: listIntegrations,
  })
  const hasAny = (data ?? []).some((i) => i.configured && i.enabled)
  return { hasAny, isLoading }
}
