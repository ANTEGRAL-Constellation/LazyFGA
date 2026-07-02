CREATE TABLE "idp_connection" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"provider" text NOT NULL,
	"signing_secret" text NOT NULL,
	"enabled" boolean DEFAULT true NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "idp_connection_provider_unique" UNIQUE("provider")
);
--> statement-breakpoint
CREATE TABLE "idp_mapping_rule" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"connection_id" uuid NOT NULL,
	"event_type" text NOT NULL,
	"match" jsonb DEFAULT '[]'::jsonb NOT NULL,
	"tuple_template" jsonb NOT NULL,
	"op" text NOT NULL,
	"priority" integer DEFAULT 0 NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL
);
--> statement-breakpoint
ALTER TABLE "idp_mapping_rule" ADD CONSTRAINT "idp_mapping_rule_connection_id_idp_connection_id_fk" FOREIGN KEY ("connection_id") REFERENCES "public"."idp_connection"("id") ON DELETE cascade ON UPDATE no action;