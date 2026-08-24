/*
 * The only piece of TypeScript in the provisioning path, and deliberately the
 * thinnest one possible: it is an HTTP client and nothing else.
 *
 * Every decision lives in the Go service behind it - validating the image tag
 * against what the operator's webhook will accept, the order of the steps,
 * idempotency, and what happens to the repository when the custom resource
 * cannot be applied. This file only carries the request there and turns the
 * answer into something readable in the scaffolder UI.
 */
import { coreServices, createBackendModule } from '@backstage/backend-plugin-api';
import { createTemplateAction } from '@backstage/plugin-scaffolder-node';
import { scaffolderActionsExtensionPoint } from '@backstage/plugin-scaffolder-node/alpha';

type ScaffoldResponse = {
  name?: string;
  repository?: { url?: string; created?: boolean; contentPushed?: boolean };
  webapp?: { namespace?: string; name?: string; applied?: boolean };
  error?: string;
  problems?: string[];
  failedStep?: string;
  detail?: string;
};

/**
 * The action id is camelCase after the colon on purpose. A hyphen inside a step
 * id or an action reference is parsed as subtraction by the template expression
 * language, so `${{ steps.scaffold-web-app.output.repoUrl }}` evaluates as
 * arithmetic and silently yields NaN.
 */
export const scaffoldWebAppActionId = 'platform:scaffoldWebApp';

export function createScaffoldWebAppAction(baseUrl: string) {
  return createTemplateAction({
    id: scaffoldWebAppActionId,
    description:
      'Asks the platform scaffolder service to create the GitHub repository and apply the matching WebApp custom resource.',
    schema: {
      input: {
        name: z => z.string().describe('Service name; also the repository and custom resource name'),
        image: z => z.string().describe('Container image with an explicit, non-latest tag'),
        port: z => z.number().describe('Port the service listens on'),
        replicas: z => z.number().describe('Desired replica count'),
        owner: z => z.string().optional().describe('GitHub account that will own the repository'),
        repoUrl: z => z.string().optional().describe('Repository location from the RepoUrlPicker'),
        description: z => z.string().optional().describe('One-line summary'),
        namespace: z => z.string().optional().describe('Namespace for the custom resource'),
        catalogOwner: z => z.string().optional().describe('Catalog entity that owns the component'),
      },
      output: {
        repoUrl: z => z.string().describe('HTML URL of the repository that now exists'),
        webappNamespace: z => z.string().describe('Namespace the custom resource was applied to'),
        webappName: z => z.string().describe('Name of the custom resource'),
      },
    },
    async handler(ctx) {
      const endpoint = `${baseUrl.replace(/\/$/, '')}/scaffold`;
      ctx.logger.info(`Asking the scaffolder service at ${endpoint} to provision ${ctx.input.name}`);

      let response: Response;
      try {
        response = await fetch(endpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(ctx.input),
          signal: ctx.signal,
        });
      } catch (error) {
        throw new Error(
          `Could not reach the scaffolder service at ${endpoint}. Is it deployed? ` +
            `(make scaffolder-deploy). Cause: ${error}`,
        );
      }

      const body = (await response.json().catch(() => ({}))) as ScaffoldResponse;

      // 400: nothing was created. Show every problem at once, which is what the
      // service returns them for.
      if (response.status === 400) {
        const problems = body.problems?.length ? `\n  - ${body.problems.join('\n  - ')}` : '';
        throw new Error(`The request was rejected before anything was created: ${body.error}${problems}`);
      }

      // 207: the repository exists but the custom resource does not. This is a
      // real, documented state, not a generic failure, so it is reported as one.
      if (response.status === 207) {
        throw new Error(
          `Provisioning stopped at the "${body.failedStep}" step. ` +
            `The repository ${body.repository?.url} was created and left in place. ` +
            `${body.detail ?? ''} Original error: ${body.error}`,
        );
      }

      if (!response.ok) {
        throw new Error(`The scaffolder service returned ${response.status}: ${body.error ?? 'no detail'}`);
      }

      const repoUrl = body.repository?.url;
      const namespace = body.webapp?.namespace;
      const name = body.webapp?.name;
      if (!repoUrl || !namespace || !name) {
        throw new Error(`The scaffolder service returned an incomplete response: ${JSON.stringify(body)}`);
      }

      ctx.logger.info(
        `Repository ${repoUrl} ${body.repository?.created ? 'created' : 'already existed'}; ` +
          `WebApp ${namespace}/${name} applied`,
      );

      ctx.output('repoUrl', repoUrl);
      ctx.output('webappNamespace', namespace);
      ctx.output('webappName', name);
    },
  });
}

/**
 * Registers the action with the scaffolder. The service URL comes from config
 * so it is not hard-coded to the local NodePort.
 */
export const scaffolderModuleIdp = createBackendModule({
  pluginId: 'scaffolder',
  moduleId: 'idp',
  register(env) {
    env.registerInit({
      deps: {
        scaffolder: scaffolderActionsExtensionPoint,
        config: coreServices.rootConfig,
      },
      async init({ scaffolder, config }) {
        const baseUrl = config.getString('platform.scaffolderBaseUrl');
        scaffolder.addActions(createScaffoldWebAppAction(baseUrl));
      },
    });
  },
});

export default scaffolderModuleIdp;
