// Renders an article's syntax tree with the design system. The tree was made and checked at
// build time (tools/codegen/web-help.mjs), so no HTML string ever reaches the page.
import { useI18n } from '@ownsolid/i18n';
import { CodeBlock, Table, Td, Th, Tr } from '@ownsolid/ui';
import { Link } from '@tanstack/react-router';
import type {
  Heading,
  PhrasingContent,
  Root,
  RootContent,
  Table as TableNode,
} from '@ownsolid/help';
import type { ReactNode } from 'react';

const linkClasses = 'font-semibold text-accent-ink hover:underline';

/** The anchor the generator gave a heading. */
function headingId(node: Heading): string | undefined {
  return (node.data as { id?: string } | undefined)?.id;
}

/** The visible text of a node, for a code block's label. */
function plain(node: RootContent): string {
  if ('value' in node) return node.value;
  if ('children' in node) return node.children.map(plain).join('');
  return '';
}

/** A link inside an article: a page of this entrance, or another article (`help:slug#anchor`). */
function ArticleLink({ url, children }: { url: string; children: ReactNode }) {
  if (url.startsWith('help:')) {
    const [slug = '', hash] = url.slice('help:'.length).split('#');
    return (
      <Link to="/help/$slug" params={{ slug }} {...(hash ? { hash } : {})} className={linkClasses}>
        {children}
      </Link>
    );
  }
  return (
    <Link to={url} className={linkClasses}>
      {children}
    </Link>
  );
}

function ArticleTable({ node }: { node: TableNode }) {
  const [head, ...rows] = node.children;
  return (
    <div className="my-4 overflow-x-auto rounded-card border border-border">
      <Table>
        {head ? (
          <thead>
            <tr>
              {head.children.map((cell, index) => (
                <Th key={index}>{cell.children.map(phrasing)}</Th>
              ))}
            </tr>
          </thead>
        ) : null}
        <tbody>
          {rows.map((row, index) => (
            <Tr key={index}>
              {row.children.map((cell, column) => (
                <Td key={column} className="whitespace-normal">
                  {cell.children.map(phrasing)}
                </Td>
              ))}
            </Tr>
          ))}
        </tbody>
      </Table>
    </div>
  );
}

function phrasing(node: PhrasingContent, index: number): ReactNode {
  switch (node.type) {
    case 'text':
      return node.value;
    case 'emphasis':
      return <em key={index}>{node.children.map(phrasing)}</em>;
    case 'strong':
      return (
        <strong key={index} className="font-semibold text-text">
          {node.children.map(phrasing)}
        </strong>
      );
    case 'inlineCode':
      return (
        <code
          key={index}
          className="rounded-[5px] bg-surface-2 px-1 py-0.5 font-mono text-[12.5px]"
        >
          {node.value}
        </code>
      );
    case 'link':
      return (
        <ArticleLink key={index} url={node.url}>
          {node.children.map(phrasing)}
        </ArticleLink>
      );
    case 'break':
      return <br key={index} />;
    default:
      return null;
  }
}

function Block({ node }: { node: RootContent }): ReactNode {
  const { t } = useI18n();
  switch (node.type) {
    case 'heading': {
      const id = headingId(node);
      const content = node.children.map(phrasing);
      return node.depth === 2 ? (
        <h2
          id={id}
          className="m-0 mt-7 mb-2 scroll-mt-20 text-[18px] font-[650] tracking-[-0.01em]"
        >
          {content}
        </h2>
      ) : (
        <h3 id={id} className="m-0 mt-5 mb-1.5 scroll-mt-20 text-[15px] font-[650]">
          {content}
        </h3>
      );
    }
    case 'paragraph':
      return <p className="m-0 my-2.5 leading-[1.65]">{node.children.map(phrasing)}</p>;
    case 'list': {
      const items = node.children.map((item, index) => (
        <li key={index} className="my-1 pl-1 leading-[1.65] [&>p]:my-0">
          {item.children.map((child, position) => (
            <Block key={position} node={child} />
          ))}
        </li>
      ));
      return node.ordered ? (
        <ol className="my-2.5 list-decimal pl-6" start={node.start ?? undefined}>
          {items}
        </ol>
      ) : (
        <ul className="my-2.5 list-disc pl-6">{items}</ul>
      );
    }
    case 'code':
      return <CodeBlock className="my-3" code={node.value} label={node.lang ?? t('help.code')} />;
    case 'table':
      return <ArticleTable node={node} />;
    case 'blockquote':
      return (
        <blockquote className="m-0 my-3 border-l-[3px] border-border-strong pl-4 text-text-2">
          {node.children.map((child, index) => (
            <Block key={index} node={child} />
          ))}
        </blockquote>
      );
    case 'thematicBreak':
      return <hr className="my-6 border-0 border-t border-border" />;
    default:
      // The generator refuses every other node type; text that slips through still shows.
      return plain(node) ? <p className="m-0 my-2.5">{plain(node)}</p> : null;
  }
}

/** An article's body. */
export function ArticleContent({ tree }: { tree: Root }) {
  return (
    <div className="max-w-[720px] text-[14.5px] text-text">
      {tree.children.map((node, index) => (
        <Block key={index} node={node} />
      ))}
    </div>
  );
}
