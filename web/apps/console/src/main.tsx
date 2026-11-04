import { startApplication } from '@ownsolid/shell';
import './index.css';
import { queryClient, router } from './router.ts';

const root = document.getElementById('root');
if (!root) throw new Error('index.html has no #root element');
startApplication({ router, queryClient, root });
