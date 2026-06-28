-- 단일 postgres 인스턴스 안에서 OpenFGA 전용 데이터베이스/유저를 분리 생성한다.
-- (데이터 소유 분리 원칙: lazyfga DB = 의도/정책/감사, openfga DB = model/tuple)
-- POSTGRES_DB=lazyfga 는 컨테이너가 자동 생성하므로 여기선 openfga 쪽만 만든다.
CREATE USER openfga WITH PASSWORD 'openfga';
CREATE DATABASE openfga OWNER openfga;
GRANT ALL PRIVILEGES ON DATABASE openfga TO openfga;
