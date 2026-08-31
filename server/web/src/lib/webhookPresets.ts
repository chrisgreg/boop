export const webhookPresets = {
  Slack: {
    payload_mode: 'custom' as const,
    body_template: '{"text": {{json .Title}}}',
    headers: '{\n  "Content-Type": "application/json"\n}',
  },
  Discord: {
    payload_mode: 'custom' as const,
    body_template: '{"content": {{json .Title}}}',
    headers: '{\n  "Content-Type": "application/json"\n}',
  },
}
