// 레퍼런스 모델 픽스처(테스트·데모용). 메인 배럴(`@lazyfga/shared`)에는 포함하지 않고
// 서브패스(`@lazyfga/shared/fixtures`)로만 노출한다.
import type { ModelIR } from "./model";
import docFolderTeam from "./__fixtures__/doc-folder-team.ir.json";

/** CONCEPT 예시: folder/document/team 모델(5-primitive IR). */
export const docFolderTeamIR: ModelIR = docFolderTeam as unknown as ModelIR;
