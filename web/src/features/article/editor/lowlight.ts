import { createLowlight } from "lowlight";
import {
  codeLanguages,
  codeLanguageAliases,
} from "../../../components/codeHighlight";

export const lowlight = createLowlight(codeLanguages);
for (const [alias, language] of Object.entries(codeLanguageAliases)) {
  lowlight.registerAlias(language, alias);
}
