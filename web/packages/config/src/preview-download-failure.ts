import type { Plugin } from 'vite';

/** Private preview fixture cookie containing the encoded JSON asset path to refuse. */
export const previewDownloadFailureCookie = 'ownsolid-preview-download-failure';

/** Produces a real file-download failure, isolated by browser cookies, in Vite preview only. */
export function previewDownloadFailurePlugin(): Plugin {
  return {
    name: 'ownsolid-preview-download-failure',
    apply: 'serve',
    configurePreviewServer(server) {
      server.middlewares.use((request, response, next) => {
        const path = (request.url ?? '').split('?')[0] ?? '';
        const refusal = `${previewDownloadFailureCookie}=${encodeURIComponent(path)}`;
        const enabled = (request.headers.cookie ?? '')
          .split(';')
          .some((cookie) => cookie.trim() === refusal);
        if (request.method !== 'GET' || !/^\/assets\/[\w-]+\.json$/.test(path) || !enabled) {
          next();
          return;
        }
        response.statusCode = 503;
        response.setHeader('Content-Type', 'text/plain; charset=utf-8');
        response.setHeader('Cache-Control', 'no-store');
        response.end('Temporary preview download failure.');
      });
    },
  };
}
