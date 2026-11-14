// The shapes of the generated help (tools/codegen/web-help.mjs).

/** The two browser entrances; each has its own articles and its own API reference. */
export type Entrance = 'console' | 'portal';

/** The languages help is written in, as the interface names them. */
export type HelpLocale = 'en' | 'zh-CN';

/** Where an article sits and the pages it explains, the same in every language. */
export interface ArticleMeta {
  slug: string;
  /** Groups articles on the help page; its name is `help.topic.<topic>` in the catalogs. */
  topic: string;
  /** Route paths whose help link opens this article. */
  pages: string[];
}

/** One section of an article, for search and the in-page contents. */
export interface ArticleSection {
  title: string;
  anchor: string;
}

/** What an article says about itself in one language. */
export interface ArticleText {
  title: string;
  summary: string;
  sections: ArticleSection[];
}

/** One operation, as the reference index lists it. */
export interface OperationSummary {
  id: string;
  method: string;
  path: string;
}

/** One contract's operations, as the reference index lists them. */
export interface ApiDomainSummary {
  domain: string;
  operations: OperationSummary[];
}

/** Everything the help pages list without loading an article or a contract. */
export interface HelpIndex {
  articles: Record<Entrance, ArticleMeta[]>;
  text: Record<Entrance, Record<HelpLocale, Record<string, ArticleText>>>;
  api: Record<Entrance, ApiDomainSummary[]>;
}

/** The parts of a JSON schema the reference shows. `$ref` names a schema of the same contract. */
export interface ApiSchema {
  $ref?: string;
  type?: string | string[];
  format?: string;
  description?: string;
  enum?: unknown[];
  const?: unknown;
  required?: string[];
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  maxItems?: number;
  properties?: Record<string, ApiSchema>;
  items?: ApiSchema;
  anyOf?: ApiSchema[];
  oneOf?: ApiSchema[];
  allOf?: ApiSchema[];
}

/** One parameter of an operation. */
export interface ApiParameter {
  name: string;
  in: 'path' | 'query';
  required: boolean;
  schema: ApiSchema;
  description?: string;
}

/** One operation in full. */
export interface ApiOperation extends OperationSummary {
  parameters: ApiParameter[];
  body: ApiSchema | null;
  responses: { status: string; schema: ApiSchema | null }[];
  /** Error codes by HTTP status. */
  errors: Record<string, string[]>;
}

/** One contract's reference. */
export interface ApiDomain {
  title: string;
  operations: ApiOperation[];
  schemas: Record<string, ApiSchema>;
}
