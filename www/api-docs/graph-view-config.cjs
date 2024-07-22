/** @type {import("@rtk-query/codegen-openapi").ConfigFile} */

const config = {
  schemaFile: "http://127.0.0.1:58000/api/openapi.json",
  apiFile: "../src/services/graph-api.ts",
  apiImport: "graphApi",
  outputFile: "../src/services/graphApiGen.ts",
  // outputFiles: {
  //   "../src/features/meta/meta-slice1.ts": {
  //     filterEndpoints: ["meta"],
  //   },
  //   "../src/features/analytics/analytics-slice1.ts": {
  //     filterEndpoints: ["analytics"],
  //   },
  //   "../src/features/channel/channels-slice1.ts": {
  //     filterEndpoints: ["channels"],
  //   },
  //   "../src/features/event/events-slice1.ts": {
  //     filterEndpoints: ["events"],
  //   },
  // },
  exportName: "graphApiGen",
  hooks: true,
};

module.exports = config;
