import { Entity } from '@backstage/catalog-model';
import { discoveryApiRef, fetchApiRef } from '@backstage/core-plugin-api';
import { EntityProvider } from '@backstage/plugin-catalog-react';
import { TestApiProvider, renderInTestApp } from '@backstage/test-utils';
import { screen, waitFor } from '@testing-library/react';
import { WebAppStatusContent } from './WebAppStatusContent';
import { WEBAPP_ANNOTATION, WebAppStatus } from './types';

const entity = (annotations?: Record<string, string>): Entity => ({
  apiVersion: 'backstage.io/v1alpha1',
  kind: 'Component',
  metadata: { name: 'my-api', annotations },
  spec: { type: 'service' },
});

const linked = entity({ [WEBAPP_ANNOTATION]: 'idp-apps/my-api' });

const status = (overrides: Partial<WebAppStatus> = {}): WebAppStatus => ({
  name: 'my-api',
  namespace: 'idp-apps',
  available: true,
  condition: { status: 'True', reason: 'DeploymentReady', message: 'all replicas are ready' },
  replicas: { desired: 3, effective: 3, ready: 3 },
  image: { desired: 'ghcr.io/mampiz/my-api:1.0.0', deployed: 'ghcr.io/mampiz/my-api:1.0.0' },
  port: 8080,
  deploymentName: 'my-api-deployment',
  ...overrides,
});

function render(target: Entity, fetchMock: jest.Mock) {
  const discovery = { getBaseUrl: async () => 'http://backend/api/proxy' };
  return renderInTestApp(
    <TestApiProvider apis={[[discoveryApiRef, discovery], [fetchApiRef, { fetch: fetchMock }]]}>
      <EntityProvider entity={target}>
        <WebAppStatusContent />
      </EntityProvider>
    </TestApiProvider>,
  );
}

function respond(body: unknown, init: { status?: number } = {}) {
  const code = init.status ?? 200;
  return {
    ok: code >= 200 && code < 300,
    status: code,
    statusText: 'test',
    json: async () => body,
  };
}

describe('WebAppStatusContent', () => {
  afterEach(() => jest.useRealTimers());

  it('shows the live state of the custom resource', async () => {
    const fetchMock = jest.fn().mockResolvedValue(respond(status()));

    await render(linked, fetchMock);

    await waitFor(() => expect(screen.getByText(/Available/)).toBeInTheDocument());
    expect(screen.getByText('3 / 3 ready')).toBeInTheDocument();
    expect(screen.getByText('ghcr.io/mampiz/my-api:1.0.0')).toBeInTheDocument();
    expect(screen.getByText('my-api-deployment')).toBeInTheDocument();

    expect(fetchMock).toHaveBeenCalledWith('http://backend/api/proxy/webapp-status/api/webapps/idp-apps/my-api');
  });

  // desired and effective are different numbers whenever the HPA owns the
  // replica count, and collapsing them would misreport what is happening.
  it('distinguishes what was asked for from what the autoscaler wants', async () => {
    const fetchMock = jest.fn().mockResolvedValue(
      respond(status({ replicas: { desired: 2, effective: 7, ready: 5 } })),
    );

    await render(linked, fetchMock);

    await waitFor(() =>
      expect(screen.getByText(/5 \/ 7 ready .*asks for 2.*autoscaler currently wants 7/)).toBeInTheDocument(),
    );
  });

  it('shows a rollout in progress', async () => {
    const fetchMock = jest.fn().mockResolvedValue(
      respond(status({ image: { desired: 'my-api:2.0.0', deployed: 'my-api:1.0.0' } })),
    );

    await render(linked, fetchMock);

    await waitFor(() =>
      expect(screen.getByText('my-api:1.0.0 running, rolling out to my-api:2.0.0')).toBeInTheDocument(),
    );
  });

  it('reports an unavailable condition rather than hiding it', async () => {
    const fetchMock = jest.fn().mockResolvedValue(
      respond(
        status({
          available: false,
          condition: { status: 'False', reason: 'DeploymentNotReady', message: '1/3 replicas ready' },
          replicas: { desired: 3, effective: 3, ready: 1 },
        }),
      ),
    );

    await render(linked, fetchMock);

    await waitFor(() => expect(screen.getByText(/Not available/)).toBeInTheDocument());
    expect(screen.getByText('1/3 replicas ready')).toBeInTheDocument();
  });

  it('explains what to do when the entity is not linked to a WebApp', async () => {
    const fetchMock = jest.fn();

    await render(entity(), fetchMock);

    expect(await screen.findByText(/not linked to a WebApp/)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('says so when the custom resource is not in the cluster', async () => {
    const fetchMock = jest.fn().mockResolvedValue(respond({}, { status: 404 }));

    await render(linked, fetchMock);

    expect(await screen.findByText(/No WebApp idp-apps\/my-api in the cluster/)).toBeInTheDocument();
  });

  it('surfaces a failing status API', async () => {
    const fetchMock = jest.fn().mockResolvedValue(respond({}, { status: 502 }));

    await render(linked, fetchMock);

    await waitFor(() =>
      expect(screen.getAllByText(/The status API returned 502/).length).toBeGreaterThan(0),
    );
  });
});
