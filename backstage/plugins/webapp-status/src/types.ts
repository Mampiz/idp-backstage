/**
 * The shape the Go status API returns. Kept as a hand-written type on purpose:
 * it is a small, stable contract, and generating it would couple the frontend
 * build to the Go service's build.
 */
export type WebAppStatus = {
  name: string;
  namespace: string;
  available: boolean;
  condition?: {
    status: string;
    reason?: string;
    message?: string;
    lastTransitionTime?: string;
  };
  replicas: {
    /** What the custom resource asks for. */
    desired: number;
    /** What the Deployment is set to, which the HPA owns when autoscaling is on. */
    effective: number;
    /** How many pods are actually serving. */
    ready: number;
  };
  image: {
    desired: string;
    deployed?: string;
  };
  port: number;
  autoscaling?: {
    minReplicas: number;
    maxReplicas: number;
    cpuThresholdPercent: number;
  };
  deploymentName?: string;
  creationTimestamp?: string;
};

/** The annotation that ties a catalog entity to its custom resource. */
export const WEBAPP_ANNOTATION = 'platform.miportfolio.com/webapp';

/** Parses the "namespace/name" annotation value. */
export function parseWebAppRef(value: string | undefined): { namespace: string; name: string } | undefined {
  if (!value) {
    return undefined;
  }
  const [namespace, name] = value.split('/');
  if (!namespace || !name) {
    return undefined;
  }
  return { namespace, name };
}
