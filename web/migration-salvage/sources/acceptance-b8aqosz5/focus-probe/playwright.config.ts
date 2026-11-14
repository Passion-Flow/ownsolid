
import { browserConfig } from "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web/web/packages/config/src/playwright.ts";
const config=browserConfig({port:4311});
export default {...config,testDir:'.',outputDir:'./results',webServer:{...config.webServer,cwd:"/Users/passion./Documents/Passion's WorkSpace/Development/lane-web/web/apps/portal"}};
