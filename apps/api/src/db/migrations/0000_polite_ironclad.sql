CREATE TABLE "instance_config" (
	"id" text PRIMARY KEY DEFAULT 'singleton' NOT NULL,
	"openfga_store_id" text NOT NULL,
	"created_at" timestamp with time zone DEFAULT now() NOT NULL,
	"updated_at" timestamp with time zone DEFAULT now() NOT NULL
);
