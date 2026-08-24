import { createFrontendPlugin } from '@backstage/frontend-plugin-api';
import { EntityContentBlueprint } from '@backstage/plugin-catalog-react/alpha';
import { WEBAPP_ANNOTATION } from './types';

/**
 * Adds a "WebApp" tab to the entity page, showing the live state of the custom
 * resource that entity runs as.
 *
 * The tab only appears on entities that carry the annotation, so components that
 * are not deployed this way do not grow an empty tab.
 */
const webAppStatusContent = EntityContentBlueprint.make({
  name: 'webapp-status',
  params: {
    path: 'webapp',
    title: 'WebApp',
    filter: (entity: { metadata: { annotations?: Record<string, string> } }) =>
      Boolean(entity.metadata.annotations?.[WEBAPP_ANNOTATION]),
    loader: () => import('./WebAppStatusContent').then(m => <m.WebAppStatusContent />),
  },
});

export const webAppStatusPlugin = createFrontendPlugin({
  pluginId: 'webapp-status',
  extensions: [webAppStatusContent],
});

export default webAppStatusPlugin;
