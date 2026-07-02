CREATE TABLE "model_version" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"authorization_model_id" text NOT NULL,
	"ir_json" jsonb NOT NULL,
	"dsl" text NOT NULL,
	"note" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"created_by" text NOT NULL
);
--> statement-breakpoint
ALTER TABLE "instance_config" ADD COLUMN "current_model_version_id" uuid;--> statement-breakpoint
ALTER TABLE "instance_config" ADD CONSTRAINT "instance_config_current_model_version_id_model_version_id_fk" FOREIGN KEY ("current_model_version_id") REFERENCES "public"."model_version"("id") ON DELETE no action ON UPDATE no action;