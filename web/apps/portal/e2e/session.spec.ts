import { registrationFlows, sessionFlows } from '@ownsolid/shell/testing';

sessionFlows({
  audience: 'portal',
  subtitle: 'Sign in to use the models and applications of your workspace.',
});

registrationFlows('portal');
