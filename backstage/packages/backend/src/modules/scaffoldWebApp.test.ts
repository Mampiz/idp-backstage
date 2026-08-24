/*
 * The action is a transport layer, so what is worth testing is exactly that:
 * that it sends what the service expects, and that each answer the service can
 * give turns into something a person reading the scaffolder log can act on.
 */
import { createScaffoldWebAppAction } from './scaffoldWebApp';

const baseUrl = 'http://scaffolder.test';

function context(overrides: Record<string, unknown> = {}) {
  const outputs: Record<string, unknown> = {};
  return {
    ctx: {
      input: {
        name: 'my-api',
        image: 'ghcr.io/mampiz/my-api:0.1.0',
        port: 8080,
        replicas: 2,
        ...overrides,
      },
      logger: { info: jest.fn(), warn: jest.fn(), error: jest.fn() },
      output: (key: string, value: unknown) => {
        outputs[key] = value;
      },
      signal: undefined,
    },
    outputs,
  };
}

function respondWith(status: number, body: unknown) {
  return jest.fn().mockResolvedValue({
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  });
}

async function run(fetchMock: jest.Mock, overrides?: Record<string, unknown>) {
  global.fetch = fetchMock as unknown as typeof fetch;
  const action = createScaffoldWebAppAction(baseUrl);
  const { ctx, outputs } = context(overrides);
  await (action.handler as unknown as (c: unknown) => Promise<void>)(ctx);
  return { outputs, fetchMock };
}

describe('platform:scaffoldWebApp', () => {
  afterEach(() => jest.restoreAllMocks());

  it('posts the inputs and exposes the outputs later steps need', async () => {
    const fetchMock = respondWith(201, {
      name: 'my-api',
      repository: { url: 'https://github.com/Mampiz/my-api', created: true },
      webapp: { namespace: 'idp-apps', name: 'my-api', applied: true },
    });

    const { outputs } = await run(fetchMock);

    expect(fetchMock).toHaveBeenCalledWith(
      'http://scaffolder.test/scaffold',
      expect.objectContaining({ method: 'POST' }),
    );
    const sent = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(sent).toMatchObject({ name: 'my-api', port: 8080, replicas: 2 });

    expect(outputs).toEqual({
      repoUrl: 'https://github.com/Mampiz/my-api',
      webappNamespace: 'idp-apps',
      webappName: 'my-api',
    });
  });

  // The service reports every validation problem at once so the form does not
  // have to be resubmitted one mistake at a time; all of them must reach the log.
  it('surfaces every validation problem from a 400', async () => {
    const fetchMock = respondWith(400, {
      error: 'invalid request',
      problems: ['image "nginx:latest" uses the mutable "latest" tag', 'port 0 must be between 1 and 65535'],
    });

    await expect(run(fetchMock)).rejects.toThrow(/latest.*\n.*port 0/s);
  });

  // A half-finished run is a documented state, not a generic failure: the log
  // has to say which step failed and that the repository was left in place.
  it('explains a 207 partial provisioning', async () => {
    const fetchMock = respondWith(207, {
      error: 'provisioning stopped at webapp: admission denied',
      failedStep: 'webapp',
      repository: { url: 'https://github.com/Mampiz/my-api', created: true },
      detail: 'Re-send the same request to finish.',
    });

    await expect(run(fetchMock)).rejects.toThrow(/webapp.*left in place.*Re-send/s);
  });

  it('reports an unreachable service as such', async () => {
    const fetchMock = jest.fn().mockRejectedValue(new Error('ECONNREFUSED'));

    await expect(run(fetchMock)).rejects.toThrow(/Could not reach the scaffolder service/);
  });

  it('rejects a success response that is missing what later steps need', async () => {
    const fetchMock = respondWith(201, { name: 'my-api', repository: {} });

    await expect(run(fetchMock)).rejects.toThrow(/incomplete response/);
  });
});
