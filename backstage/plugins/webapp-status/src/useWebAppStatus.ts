import { useCallback, useEffect, useRef, useState } from 'react';
import { discoveryApiRef, fetchApiRef, useApi } from '@backstage/core-plugin-api';
import { WebAppStatus } from './types';

/**
 * Polling interval. Polling rather than SSE, deliberately: the state being shown
 * is a handful of fields that change on the timescale of a rollout, the Go API
 * already serves them from an informer cache so a request costs nothing on the
 * cluster, and an open stream per viewer would have to be reconnected through
 * the Backstage proxy on every hiccup. If this ever needs sub-second latency,
 * the Go service is where the stream would belong, not the browser.
 */
export const POLL_INTERVAL_MS = 5000;

export type WebAppStatusState = {
  status?: WebAppStatus;
  error?: Error;
  loading: boolean;
  /** Whether the custom resource is simply not in the cluster. */
  notFound: boolean;
};

export function useWebAppStatus(ref: { namespace: string; name: string } | undefined): WebAppStatusState {
  const discovery = useApi(discoveryApiRef);
  const fetchApi = useApi(fetchApiRef);

  const [state, setState] = useState<WebAppStatusState>({ loading: true, notFound: false });
  // Kept in a ref so an in-flight response from a previous entity cannot land
  // on the current one after a navigation.
  const current = useRef(0);

  const load = useCallback(async () => {
    if (!ref) {
      return;
    }
    const generation = ++current.current;
    try {
      const base = await discovery.getBaseUrl('proxy');
      const response = await fetchApi.fetch(
        `${base}/webapp-status/api/webapps/${encodeURIComponent(ref.namespace)}/${encodeURIComponent(ref.name)}`,
      );

      if (generation !== current.current) {
        return;
      }
      if (response.status === 404) {
        setState({ loading: false, notFound: true });
        return;
      }
      if (!response.ok) {
        throw new Error(`The status API returned ${response.status} ${response.statusText}`);
      }
      setState({ status: (await response.json()) as WebAppStatus, loading: false, notFound: false });
    } catch (error) {
      if (generation === current.current) {
        setState({ error: error as Error, loading: false, notFound: false });
      }
    }
  }, [discovery, fetchApi, ref]);

  useEffect(() => {
    if (!ref) {
      setState({ loading: false, notFound: false });
      return undefined;
    }
    load();
    const timer = setInterval(load, POLL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [load, ref]);

  return state;
}
