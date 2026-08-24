import { parseWebAppRef } from './types';

describe('parseWebAppRef', () => {
  it('splits a namespace/name annotation', () => {
    expect(parseWebAppRef('idp-apps/my-api')).toEqual({ namespace: 'idp-apps', name: 'my-api' });
  });

  it.each([undefined, '', 'my-api', '/my-api', 'idp-apps/'])('rejects %p', value => {
    expect(parseWebAppRef(value)).toBeUndefined();
  });
});
