CREATE TABLE "policy" (
	"id" text PRIMARY KEY NOT NULL,
	"permission" text NOT NULL,
	"resource_type" text NOT NULL,
	"description" text,
	"condition_ref" text,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL,
	CONSTRAINT "policy_perm_resource_unq" UNIQUE("permission","resource_type")
);
--> statement-breakpoint
CREATE TABLE "service_token" (
	"id" uuid PRIMARY KEY DEFAULT gen_random_uuid() NOT NULL,
	"name" text NOT NULL,
	"token_hash" text NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"last_used_at" timestamp with time zone,
	"revoked_at" timestamp with time zone
);
