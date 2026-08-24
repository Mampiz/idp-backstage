/*
 * A separate config from the repository's playwright.config.ts on purpose: this
 * one records video, runs a single scripted walkthrough, and is not part of any
 * verifier. Recording a demo and asserting a claim are different jobs and should
 * not share a config.
 */
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 300_000,
  expect: { timeout: 60_000 },
  retries: 0,
  workers: 1,
  reporter: [['line']],
  outputDir: 'recording',
  use: {
    baseURL: process.env.PLAYWRIGHT_URL ?? 'http://localhost:3000',
    viewport: { width: 1280, height: 720 },
    video: { mode: 'on', size: { width: 1280, height: 720 } },
    actionTimeout: 60_000,
    ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}),
  },
});
