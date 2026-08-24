/*
 * Captures the screenshots used in the README and the documentation.
 *
 * They are generated rather than taken by hand so they can be regenerated when
 * the UI changes, instead of quietly ageing into a picture of software that no
 * longer looks like this.
 */
import { test, expect } from '@playwright/test';

const NAME = process.env.DEMO_NAME ?? 'idp-demo-service';
const OUT = '../docs/assets';

test.use({ viewport: { width: 1440, height: 900 }, video: 'off' });

test.beforeEach(async ({ page }) => {
  await page.goto('/');
  const enter = page.getByRole('button', { name: 'Enter' });
  await enter.click({ timeout: 60_000 }).catch(() => {});
  // Wait for the session to actually be established before navigating away.
  // Navigating immediately after the click cancels the sign-in and the next
  // page renders the card again.
  await expect(enter).toBeHidden({ timeout: 60_000 });
});

test('catalog', async ({ page }) => {
  // The catalog is mounted at / by the page:catalog path override in
  // app-config.yaml, so /catalog is not a route in this app.
  await page.goto('/');
  await expect(page.getByText('WebApp Operator')).toBeVisible({ timeout: 60_000 });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${OUT}/catalog.png` });
});

test('the template form', async ({ page }) => {
  await page.goto('/create');
  await expect(page.getByText('Go service running on Kubernetes')).toBeVisible({ timeout: 60_000 });
  await page.getByRole('button', { name: /choose/i }).first().click();

  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('payments-api');
  await page.getByRole('textbox', { name: 'Description', exact: true }).fill(
    'Handles payment intents and refunds.',
  );
  const owner = page.getByRole('textbox', { name: 'Owner', exact: true });
  await owner.click();
  await owner.fill('platform');
  await page.waitForTimeout(700);
  await page
    .getByRole('option', { name: /platform/i })
    .first()
    .click()
    .catch(() => {});
  await page.waitForTimeout(1200);
  await page.screenshot({ path: `${OUT}/template-form.png` });
});

test('the WebApp tab', async ({ page }) => {
  await page.goto(`/catalog/default/component/${NAME}/webapp`);
  await expect(page.getByText(/ready/).first()).toBeVisible({ timeout: 60_000 });
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${OUT}/webapp-tab.png` });
});

test('techdocs', async ({ page }) => {
  await page.goto('/docs/default/component/idp-backstage');
  await expect(page.getByText(/Internal Developer Platform/i).first()).toBeVisible({ timeout: 180_000 });
  await page.waitForTimeout(3000);
  await page.screenshot({ path: `${OUT}/techdocs.png` });
});
