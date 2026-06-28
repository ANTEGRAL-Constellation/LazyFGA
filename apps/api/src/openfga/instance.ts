import { config } from "../config";
import { createOpenFgaGateway } from "./gateway";

/** 프로세스 단일 OpenFGA 게이트웨이. index.ts가 부팅 시 bootstrap()한다. */
export const gateway = createOpenFgaGateway({ apiUrl: config.openfgaApiUrl });
