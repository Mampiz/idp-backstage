import { createApp } from '@backstage/frontend-defaults';
import catalogPlugin from '@backstage/plugin-catalog/alpha';
// Adds the "WebApp" tab to entities that run as a WebApp custom resource.
import webAppStatusPlugin from '@internal/plugin-webapp-status';
import { navModule } from './modules/nav';
import { homeModule } from './modules/home';

export default createApp({
  features: [catalogPlugin, webAppStatusPlugin, navModule, homeModule],
});
