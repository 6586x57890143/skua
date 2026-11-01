-- Whether /pp is open to every member of a server, not only its admins and
-- the beta role. An admin opens it once from /pp's help page.
alter table pp_guild add column open boolean not null default false;
