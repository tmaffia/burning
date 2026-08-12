# Burning

Burning reports how much coding-agent subscription capacity has been consumed and when it renews.

## Language

**Usage**:
The percentage of a subscription's metered capacity consumed within a usage window. A full usage bar means the allowance is exhausted.
_Avoid_: Token count, remaining usage

**Remaining Allowance**:
The percentage of metered capacity still available within a usage window; the complement of usage.
_Avoid_: Remaining tokens, token balance

**Usage Window**:
A provider-defined period over which an allowance is measured, identified by its actual duration and reset time when available.
_Avoid_: Daily limit, fixed five-hour limit

**Provider**:
A subscription service that defines and reports usage allowances. Burning initially supports OpenAI Codex and Ollama Cloud.
_Avoid_: Model, API vendor
