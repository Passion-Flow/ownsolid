import { useI18n } from '@ownsolid/i18n';
import { Button, Menu, MenuCheckboxItem, MenuLabel } from '@ownsolid/ui';
import { Columns3 } from 'lucide-react';
import type { ListView } from './use-list-view.ts';

/** Shows and hides a list's columns; the column that names a row stays. */
export function ColumnChooser({ list }: { list: ListView }) {
  const { t } = useI18n();
  return (
    <Menu
      trigger={
        <Button aria-label={t('list.columns.label')} title={t('list.columns.label')}>
          <Columns3 aria-hidden />
          {t('list.columns')}
        </Button>
      }
    >
      <MenuLabel>{t('list.columns.label')}</MenuLabel>
      {list.definition.columns.map((column) => (
        <MenuCheckboxItem
          key={column.id}
          disabled={column.locked === true}
          checked={list.isColumnVisible(column.id)}
          onCheckedChange={(checked) => {
            list.setColumnVisible(column.id, checked);
          }}
        >
          {t(column.label)}
        </MenuCheckboxItem>
      ))}
    </Menu>
  );
}
