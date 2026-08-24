/*
 * The claim being tested is the one the brief makes: the WebApp tab shows the
 * real state of the cluster, and it changes when the custom resource is scaled
 * with kubectl. Everything else in this repository can be checked with curl;
 * this is the part that needs a browser, because "the tab shows it" is not the
 * same statement as "the API returns it".
 */
import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';

const CONTEXT = process.env.KUBE_CONTEXT ?? 'kind-idp-local';
const NAMESPACE = process.env.WEBAPP_NAMESPACE ?? 'idp-apps';
const NAME = process.env.TEMPLATE_DEMO_NAME ?? 'idp-template-demo';

function kubectl(...args: string[]): string {
  return execFileSync('kubectl', [`--context=${CONTEXT}`, ...args], { encoding: 'utf8' }).trim();
}

function scaleTo(replicas: number) {
  kubectl(
    '-n',
    NAMESPACE,
    'patch',
    'webapp',
    NAME,
    '--type=merge',
    '-p',
    JSON.stringify({ spec: { replicas } }),
  );
}

test.describe('the WebApp tab', () => {
  test.beforeAll(() => {
    // Fail early and clearly if the fixture the test reads is not there.
    kubectl('-n', NAMESPACE, 'get', 'webapp', NAME);
  });

  test.afterAll(() => {
    scaleTo(2);
  });

  test('shows the real state and follows a kubectl scale', async ({ page }) => {
    scaleTo(2);

    await page.goto(`/catalog/default/component/${NAME}/webapp`);

    // The tab exists on this entity because it carries the annotation.
    await expect(page.getByRole('heading', { name: 'WebApp' })).toBeVisible({ timeout: 60_000 });

    // The state shown has to be the cluster's, so it is compared against what
    // kubectl reports rather than against a fixture.
    const desired = kubectl('-n', NAMESPACE, 'get', 'webapp', NAME, '-o', 'jsonpath={.spec.replicas}');
    await expect(page.getByText(new RegExp(`/ ${desired} ready`))).toBeVisible({ timeout: 60_000 });

    const image = kubectl('-n', NAMESPACE, 'get', 'webapp', NAME, '-o', 'jsonpath={.spec.image}');
    await expect(page.getByText(image, { exact: false })).toBeVisible();

    // Now the actual claim: scale the custom resource from outside Backstage
    // entirely and watch the page follow it, with no reload.
    scaleTo(4);

    await expect(page.getByText(/\/ 4 ready/)).toBeVisible({ timeout: 60_000 });
    expect(kubectl('-n', NAMESPACE, 'get', 'webapp', NAME, '-o', 'jsonpath={.spec.replicas}')).toBe('4');

    scaleTo(2);
    await expect(page.getByText(/\/ 2 ready/)).toBeVisible({ timeout: 60_000 });
  });
});
