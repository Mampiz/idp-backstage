import {
  EmptyState,
  InfoCard,
  Progress,
  ResponseErrorPanel,
  StatusError,
  StatusOK,
  StatusPending,
  StructuredMetadataTable,
} from '@backstage/core-components';
import { useEntity } from '@backstage/plugin-catalog-react';
import Grid from '@material-ui/core/Grid';
import Typography from '@material-ui/core/Typography';
import { useMemo } from 'react';
import { POLL_INTERVAL_MS, useWebAppStatus } from './useWebAppStatus';
import { WEBAPP_ANNOTATION, WebAppStatus, parseWebAppRef } from './types';

function ConditionBadge({ status }: { status?: WebAppStatus }) {
  if (!status?.condition) {
    // The operator has not written a status yet, which is a real state and not
    // the same as "unhealthy".
    return <StatusPending>Waiting for the operator</StatusPending>;
  }
  const { status: value, reason } = status.condition;
  if (value === 'True') {
    return <StatusOK>Available{reason ? ` (${reason})` : ''}</StatusOK>;
  }
  return <StatusError>Not available{reason ? ` (${reason})` : ''}</StatusError>;
}

function replicaSummary(status: WebAppStatus): string {
  const { ready, desired, effective } = status.replicas;
  // desired and effective diverge legitimately: with autoscaling on, the
  // operator hands the Deployment's replica count to the HPA. Showing one
  // number would misrepresent what is happening.
  if (effective !== desired) {
    return `${ready} / ${effective} ready (the resource asks for ${desired}; the autoscaler currently wants ${effective})`;
  }
  return `${ready} / ${desired} ready`;
}

function imageSummary(status: WebAppStatus): string {
  if (!status.image.deployed) {
    return `${status.image.desired} (nothing running yet)`;
  }
  if (status.image.deployed !== status.image.desired) {
    return `${status.image.deployed} running, rolling out to ${status.image.desired}`;
  }
  return status.image.deployed;
}

export function WebAppStatusContent() {
  const { entity } = useEntity();
  const annotation = entity.metadata.annotations?.[WEBAPP_ANNOTATION];
  const ref = useMemo(() => parseWebAppRef(annotation), [annotation]);
  const { status, error, loading, notFound } = useWebAppStatus(ref);

  if (!ref) {
    return (
      <EmptyState
        missing="info"
        title="This component is not linked to a WebApp"
        description={`Add the "${WEBAPP_ANNOTATION}" annotation with the value "namespace/name" to catalog-info.yaml, and this tab will show the live state of the custom resource.`}
      />
    );
  }

  if (loading && !status) {
    return <Progress />;
  }

  if (error) {
    return <ResponseErrorPanel error={error} />;
  }

  if (notFound) {
    return (
      <EmptyState
        missing="data"
        title={`No WebApp ${ref.namespace}/${ref.name} in the cluster`}
        description="The catalog entity points at a custom resource that is not there. It may have been deleted, or never applied."
      />
    );
  }

  if (!status) {
    return <Progress />;
  }

  const metadata: Record<string, string> = {
    'Custom resource': `${status.namespace}/${status.name}`,
    Replicas: replicaSummary(status),
    Image: imageSummary(status),
    Port: String(status.port),
    Workload: status.deploymentName ?? 'not created yet',
  };
  if (status.autoscaling) {
    metadata.Autoscaling =
      `${status.autoscaling.minReplicas}-${status.autoscaling.maxReplicas} replicas ` +
      `at ${status.autoscaling.cpuThresholdPercent}% CPU`;
  }
  if (status.condition?.message) {
    metadata['Last report'] = status.condition.message;
  }

  return (
    <Grid container spacing={3}>
      <Grid item xs={12}>
        <InfoCard
          title="WebApp"
          subheader={<ConditionBadge status={status} />}
          deepLink={{
            title: 'Reported by the status API',
            link: '#',
          }}
        >
          <StructuredMetadataTable metadata={metadata} />
          <Typography variant="caption" color="textSecondary">
            Live from the cluster, refreshed every {POLL_INTERVAL_MS / 1000} seconds.
          </Typography>
        </InfoCard>
      </Grid>
    </Grid>
  );
}

export default WebAppStatusContent;
