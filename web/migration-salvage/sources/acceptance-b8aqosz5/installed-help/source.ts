import { execFileSync } from 'node:child_process';

/** Frozen source shared with the installed image input. */
export const sourceRevision = '4463bbe628f60486bbdb92c15f72311fb53d076a';
/** Private, detached source tree for this acceptance window. */
export const sourcePath = process.env.OWNSOLID_LIVE_WEB_SOURCE ?? "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-4463bbe6";

const actual = execFileSync('git', ['-C', sourcePath, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
const changed = execFileSync('git', ['-C', sourcePath, 'status', '--porcelain', '--untracked-files=no'], { encoding: 'utf8' }).trim();
if (actual !== sourceRevision || changed !== '') {
  throw new Error('The acceptance source must be clean and fixed to the frozen installation revision');
}
