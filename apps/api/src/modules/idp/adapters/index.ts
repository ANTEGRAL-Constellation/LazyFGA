// lazyfga-16: 빌트인 IdP adapter 등록. 부팅 시 side-effect import로 레지스트리를 채운다.
import { registerAdapter } from "../types";
import { zitadelAdapter } from "./zitadel";

registerAdapter(zitadelAdapter);
