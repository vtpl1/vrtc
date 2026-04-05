import type { ConfigFile } from '@rtk-query/codegen-openapi';

const config: ConfigFile = {
  schemaFile: '../../../docs/openapi/openapi.json',
  apiFile: './src/app/api.ts',
  apiImport: 'api',
  outputFile: './src/app/generated/openapi.ts',
  exportName: 'generatedApi',
  hooks: true,
  tag: true,
};

export default config;
