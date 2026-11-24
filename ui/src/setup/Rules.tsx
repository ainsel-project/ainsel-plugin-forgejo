import type { ChangeEvent } from "react";
import { Button, Field, Input } from "@ainsel/plugin-sdk";
import type { Input as InputType } from "./model";
import { FIELD_GRID_STYLE, StepFooter } from "./Wizard";

interface RulesProps {
  input: InputType;
  setInput: (input: InputType) => void;
  onNext: () => void;
  onBack: () => void;
}

export function Rules({
  input,
  setInput,
  onNext,
  onBack,
}: RulesProps) {
  return (
    <div className="forgejo-rules" style={{ display: "flex", flexDirection: "column", gap: "1.25rem" }}>
      <div style={FIELD_GRID_STYLE}>
        <Field
          htmlFor="rules-exclude-label"
          label="Exclude label"
          hint="Issues with this label are skipped by the refiner. Leave the default unless your org already uses another label."
        >
          <Input
            id="rules-exclude-label"
            value={input.excludeLabel}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setInput({ ...input, excludeLabel: e.target.value })}
            placeholder="do-not-refine"
          />
        </Field>
      </div>

      <StepFooter onBack={onBack}>
        <Button variant="primary" onClick={onNext} className="forgejo-next-btn">
          Continue
        </Button>
      </StepFooter>
    </div>
  );
}
