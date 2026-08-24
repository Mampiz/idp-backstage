/*
 * Records the whole flow for the README: form -> repository -> custom resource
 * -> running pods -> live status, ending with a kubectl scale that the page
 * follows.
 *
 * This is a recording, not a verifier. It still uses real everything: a real
 * GitHub repository is created and a real workload runs.
 */
import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';

const CONTEXT = process.env.KUBE_CONTEXT ?? 'kind-idp-local';
const NAMESPACE = process.env.WEBAPP_NAMESPACE ?? 'idp-apps';
const OWNER = process.env.GITHUB_OWNER ?? 'Mampiz';
const NAME = process.env.DEMO_NAME ?? 'idp-demo-service';

function kubectl(...args: string[]): string {
  return execFileSync('kubectl', [`--context=${CONTEXT}`, ...args], { encoding: 'utf8' }).trim();
}

// Pauses long enough for a viewer to read what just happened. A recording that
// is technically correct and impossible to follow is not much use.
async function beat(page: import('@playwright/test').Page, ms = 1200) {
  await page.waitForTimeout(ms);
}

test('the whole flow', async ({ page }) => {
  await page.goto('/');
  await page
    .getByRole('button', { name: 'Enter' })
    .click({ timeout: 60_000 })
    .catch(() => {});
  await beat(page);

  await page.goto('/create');
  await expect(page.getByText('Go service running on Kubernetes')).toBeVisible();
  await beat(page);

  await page.getByRole('button', { name: /choose/i }).first().click();
  await beat(page);

  // The form's fields carry accessible names rather than <label for>, so they
  // are located by role and name.
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(NAME);
  await page.getByRole('textbox', { name: 'Description', exact: true }).fill(
    'Scaffolded live from the portal.',
  );
  await beat(page);

  // OwnerPicker: a combobox over the catalog's groups and users. Typing narrows
  // it to the owning team rather than whatever happens to be first.
  const owner = page.getByRole('textbox', { name: 'Owner', exact: true });
  await owner.click();
  await owner.fill('platform');
  await beat(page, 700);
  await page
    .getByRole('option', { name: /platform/i })
    .first()
    .click()
    .catch(async () => {
      await page.getByRole('option').first().click();
    });
  await beat(page, 900);

  await page.getByRole('button', { name: 'Next', exact: true }).click();
  await beat(page, 900);

  // RepoUrlPicker: host, owner and repository.
  await page.getByRole('textbox', { name: /^owner/i }).first().fill(OWNER);
  await page.getByRole('textbox', { name: /^repository/i }).first().fill(NAME);
  await beat(page);

  // The runtime page keeps its defaults: a pinned image, port 8080, two
  // replicas. Rather than hard-coding how many pages are left, advance until
  // the review page offers Create; the form can grow a page without this
  // breaking.
  const create = page.getByRole('button', { name: /^create$/i });
  for (let i = 0; i < 5; i++) {
    if (await create.isVisible().catch(() => false)) {
      break;
    }
    await page
      .getByRole('button', { name: /^(next|review)$/i })
      .first()
      .click();
    await beat(page, 900);
  }

  await expect(create).toBeVisible();
  await beat(page, 900);
  await create.click();

  // The task page streams the steps as they run, then renders the template's
  // outputs: the repository link and what is now running in the cluster.
  await expect(page.getByRole('button', { name: /Create the repository and put it on the cluster/i })).toBeVisible({
    timeout: 180_000,
  });
  await beat(page, 2000);
  await expect(page.getByRole('heading', { name: 'Running in the cluster' })).toBeVisible({
    timeout: 180_000,
  });
  await beat(page, 3000);

  // Straight to the live state of what was just created.
  await page.goto(`/catalog/default/component/${NAME}/webapp`);
  await expect(page.getByText(`${NAMESPACE}/${NAME}`).first()).toBeVisible({ timeout: 120_000 });
  await beat(page, 2500);

  // And the point of the tab: it is the cluster, not a cached form value.
  kubectl('-n', NAMESPACE, 'patch', 'webapp', NAME, '--type=merge', '-p', '{"spec":{"replicas":4}}');
  await expect(page.getByText(/\/ 4 ready/)).toBeVisible({ timeout: 120_000 });
  await beat(page, 3000);

  kubectl('-n', NAMESPACE, 'patch', 'webapp', NAME, '--type=merge', '-p', '{"spec":{"replicas":2}}');
  await expect(page.getByText(/\/ 2 ready/)).toBeVisible({ timeout: 120_000 });
  await beat(page, 2000);
});
