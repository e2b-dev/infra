import { Template } from 'e2b';

export const TEMPLATE_MARKER_PATH = '/opt/monad/canary-template';
export const TEMPLATE_MARKER_VALUE = 'monad-gcp-canary-template-v1';

export function createCanaryTemplateDefinition() {
  return Template()
    .fromUbuntuImage('24.04')
    .runCmd(
      `install -d -m 0755 /opt/monad && printf '%s\\n' '${TEMPLATE_MARKER_VALUE}' > ${TEMPLATE_MARKER_PATH}`,
      { user: 'root' },
    );
}
