import { downloadGatewayAliasCA, isProblem } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { Button, ProblemNotice, saveFile } from '@ownsolid/ui';
import { useMutation } from '@tanstack/react-query';
import { Download } from 'lucide-react';
import { useAudience } from '../session.ts';

/** Downloads the current alias's public trust bundle on demand. */
export function GatewayCADownload({ workspace, aliasId }: { workspace: string; aliasId: string }) {
  const { t } = useI18n();
  const audience = useAudience();
  const ca = useMutation({
    mutationFn: () => downloadGatewayAliasCA(audience, workspace, aliasId),
  });
  const start = () => {
    if (ca.isPending) return;
    ca.mutate(undefined, {
      onSuccess: ({ content, name }) => {
        saveFile(content, name);
      },
    });
  };
  const missing = isProblem(ca.error, 'RESOURCE_NOT_FOUND', 'GATEWAY_AUTHORITY_UNAVAILABLE');
  return (
    <div className="flex flex-col items-start gap-2">
      <Button size="sm" aria-disabled={ca.isPending || undefined} onClick={start}>
        <Download aria-hidden />
        {t(ca.isPending ? 'gateway.download.pending' : 'gateway.use.ca')}
      </Button>
      {isProblem(ca.error) ? (
        <ProblemNotice
          problem={ca.error}
          title={missing ? t('gateway.use.ca.unavailable.title') : undefined}
          detail={missing ? t('gateway.use.ca.unavailable.detail') : undefined}
          onRetry={start}
        />
      ) : null}
    </div>
  );
}
