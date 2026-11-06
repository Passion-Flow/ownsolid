import { useI18n } from '@ownsolid/i18n';
import { Button, useToast } from '@ownsolid/ui';
import { Link2 } from 'lucide-react';

/** Copies the address, which holds the list's filters, sort and open row (UX-114). */
export function CopyLinkButton() {
  const { t } = useI18n();
  const toast = useToast();
  const copy = () => {
    navigator.clipboard.writeText(window.location.href).then(
      () => {
        toast({ message: t('list.link.copied') });
      },
      () => {
        toast({ message: t('list.link.failed'), retry: copy });
      },
    );
  };
  return (
    <Button aria-label={t('list.link')} title={t('list.link')} size="icon" onClick={copy}>
      <Link2 aria-hidden />
    </Button>
  );
}
