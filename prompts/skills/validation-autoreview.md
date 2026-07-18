### Validation and Autoreview

Validation and DarkFactory Autoreview are independent required gates. Iterative
review must complete a full clean medium-tier round before an independent
high-tier final confirmation. Any final finding returns to bounded fix and
iterative review-to-clean. Autoreview evaluates correctness and whether the
target provides adequate validation coverage; the separate exact-head Validate
gate owns command execution evidence. Results omitted from model context solely
because that independent gate owns them are not review findings. Malformed
verdicts, incomplete findings, exhausted rounds, unavailable routes, or actual
validation-coverage gaps still block closed.
